# Documentación Detallada - Implementación del Protocolo Raft

## Índice
1. [Introducción](#introducción)
2. [Constantes y Configuración](#constantes-y-configuración)
3. [Estructuras de Datos](#estructuras-de-datos)
4. [Funciones de Inicialización](#funciones-de-inicialización)
5. [Gestión de Elecciones](#gestión-de-elecciones)
6. [Transiciones de Estado](#transiciones-de-estado)
7. [Replicación de Log (Líder)](#replicación-de-log-líder)
8. [Aplicación de Operaciones](#aplicación-de-operaciones)
9. [RPCs del Protocolo Raft](#rpcs-del-protocolo-raft)
10. [API Público](#api-público)

---

## Introducción

Este código implementa el algoritmo de consenso distribuido **Raft**, diseñado para gestionar un log replicado en un conjunto de servidores. Raft garantiza que todos los nodos del sistema converjan al mismo estado, incluso en presencia de fallos.

### Conceptos Clave de Raft:
- **Líder**: Un nodo que gestiona todas las escrituras y coordina la replicación
- **Candidato**: Un nodo que está intentando convertirse en líder
- **Seguidor**: Un nodo pasivo que replica el estado del líder
- **Término (Term)**: Un período lógico de tiempo usado para detectar información obsoleta
- **Log**: Secuencia ordenada de comandos/operaciones

---

## Constantes y Configuración

```go
const (
    IntNOINICIALIZADO = -1
    kEnableDebugLogs = true
    kLogToStdout = false
    kLogOutputDir = "./logs_raft/"
    // Códigos ANSI para colorear logs...
)
```

### Descripción:
- **IntNOINICIALIZADO**: Valor centinela para indicar variables enteras sin inicializar
- **kEnableDebugLogs**: Activa/desactiva el sistema de logging (debe ser `false` en producción)
- **kLogToStdout**: Determina si los logs van a stdout o a archivos
- **kLogOutputDir**: Directorio donde se guardan los archivos de log
- **Códigos ANSI**: Colores para diferentes tipos de eventos en los logs (líderes, votos, RPCs, errores)

### Sistema de Coloreado de Logs

```go
type colorWriter struct {
    writer io.Writer
}

func (cw colorWriter) Write(p []byte) (int, error) {
    colored := applyLogColor(string(p))
    return cw.writer.Write([]byte(colored))
}
```

**Propósito**: Envuelve un `io.Writer` para aplicar colores ANSI a los logs automáticamente.

```go
func applyLogColor(s string) string {
    text := strings.TrimSuffix(s, "\n")
    color := ansiInfo
    
    switch {
    case strings.Contains(text, "es ahora LIDER"):
        color = ansiLeader
    case strings.Contains(text, "es ahora CANDIDATO"):
        color = ansiVote
    // ... más casos
    }
    
    return color + text + ansiReset + sufijo_si_hay_salto_de_linea
}
```

**Lógica**: Analiza el contenido del log y asigna un color según palabras clave (LIDER=verde, CANDIDATO=cian, Error=rojo, etc.).

---

## Estructuras de Datos

### TipoOperacion

```go
type TipoOperacion struct {
    Operacion string // "leer" o "escribir"
    Clave     string
    Valor     string // vacío para operaciones de lectura
}
```

Representa una operación del cliente que será replicada a través de Raft.

### AplicaOperacion

```go
type AplicaOperacion struct {
    Indice    int
    Operacion TipoOperacion
}
```

Mensaje enviado al canal `canalAplicar` cuando una entrada del log ha sido comprometida y puede aplicarse a la máquina de estados.

### EstadoNodoRaft

```go
type EstadoNodoRaft int

const (
    FOLLOWER EstadoNodoRaft = iota
    CANDIDATE
    LEADER
    DEAD
)
```

Enumeración de los posibles estados de un nodo Raft:
- **FOLLOWER**: Estado inicial y por defecto
- **CANDIDATE**: Estado transitorio durante elecciones
- **LEADER**: Nodo activo que coordina el sistema
- **DEAD**: Nodo apagado/terminado

### EntriesLog

```go
type EntriesLog struct {
    Term    int
    Command TipoOperacion
}
```

Entrada individual del log replicado. Cada entrada almacena:
- El **término** en el que fue creada
- El **comando** a ejecutar

### NodoRaft (Estructura Principal)

```go
type NodoRaft struct {
    Mux sync.Mutex
    Nodos []rpctimeout.HostPort
    Yo int
    IdLider int
    Logger *log.Logger
    
    // Estado Persistente (debe sobrevivir a reinicios)
    CurrentTerm int
    VotedFor int
    Log []EntriesLog
    
    // Estado Volátil
    CommitIndex int
    LastApplied int
    
    // Volátil para Líderes
    NextIndex map[int]int
    MatchIndex map[int]int
    
    // Gestión Interna
    EstadoNodo EstadoNodoRaft
    ElectionResetEvent time.Time
    AplicarOp chan AplicaOperacion
    NewCommitReadyChan chan struct{}
    ShutdownCh chan struct{}
    apagado bool
}
```

**Campos Críticos**:

1. **CurrentTerm**: Último término visto por el servidor (se incrementa en cada elección)
2. **VotedFor**: ID del candidato que recibió el voto en el término actual (-1 si ninguno)
3. **Log**: Array de entradas del log
4. **CommitIndex**: Índice de la entrada más alta comprometida (conocida como aplicable de forma segura)
5. **LastApplied**: Índice de la entrada más alta aplicada a la máquina de estados
6. **NextIndex**: Para cada servidor, índice de la próxima entrada a enviar
7. **MatchIndex**: Para cada servidor, índice de la entrada más alta replicada conocida

---

## Funciones de Inicialización

### NuevoNodo()

```go
func NuevoNodo(nodos []rpctimeout.HostPort, yo int, 
               canalAplicarOperacion chan AplicaOperacion) *NodoRaft {
    nr := &NodoRaft{}
    nr.Nodos = nodos
    nr.Yo = yo
    nr.IdLider = -1
```

**Parámetros**:
- `nodos`: Lista de direcciones IP:Puerto de todos los nodos del clúster
- `yo`: Índice de este nodo en el array `nodos`
- `canalAplicarOperacion`: Canal donde se enviarán operaciones comprometidas

**Inicialización del Logger**:

```go
if kEnableDebugLogs {
    nombreNodo := nodos[yo].Host() + "_" + nodos[yo].Port()
    
    if kLogToStdout {
        coloredStdout := colorWriter{writer: os.Stdout}
        nr.Logger = log.New(coloredStdout, nombreNodo+" -->> ",
            log.Lmicroseconds|log.Lshortfile)
    } else {
        // Crear directorio y archivo de log
        logOutputFile, err := os.OpenFile(...)
        coloredFile := colorWriter{writer: logOutputFile}
        nr.Logger = log.New(coloredFile, logPrefix+" -> ", ...)
    }
}
```

**Línea a línea**:
1. Construye un nombre único para el nodo basado en su dirección
2. Si `kLogToStdout` es true, los logs van a la consola con colores
3. Si no, crea un archivo en `kLogOutputDir` con el nombre del nodo
4. Envuelve el writer con `colorWriter` para colorear automáticamente
5. Configura el formato del log con microsegundos y archivo/línea

**Inicialización del Estado**:

```go
nr.CurrentTerm = 0
nr.VotedFor = -1
nr.Log = make([]EntriesLog, 0)

nr.CommitIndex = -1
nr.LastApplied = -1
nr.NextIndex = make(map[int]int)
nr.MatchIndex = make(map[int]int)

nr.EstadoNodo = FOLLOWER
nr.ElectionResetEvent = time.Now()

nr.AplicarOp = canalAplicarOperacion
nr.NewCommitReadyChan = make(chan struct{}, 16)
nr.ShutdownCh = make(chan struct{})
```

**Detalles**:
- Comienza en el término 0 sin haber votado por nadie
- CommitIndex y LastApplied comienzan en -1 (no hay entradas aún)
- Todos los nodos comienzan como FOLLOWERS
- `ElectionResetEvent` se inicializa con el tiempo actual para comenzar el timer de elección
- `NewCommitReadyChan` tiene buffer de 16 para evitar bloqueos

**Inicio de Gorutinas**:

```go
go nr.runElectionTimer()
go nr.commitChanSender()

return nr
```

Se lanzan dos gorutinas concurrentes:
1. `runElectionTimer()`: Monitorea timeouts de elección
2. `commitChanSender()`: Envía entradas comprometidas al canal de aplicación

---

## Gestión de Elecciones

### electionTimeout()

```go
func (nr *NodoRaft) electionTimeout() time.Duration {
    return time.Duration(150+rand.Intn(150)) * time.Millisecond
}
```

**Propósito**: Genera un timeout aleatorio entre 150-300ms para evitar elecciones simultáneas.

**Por qué es aleatorio**: Si todos los nodos tuvieran el mismo timeout, podrían iniciar elecciones simultáneamente, causando votos divididos continuamente.

### runElectionTimer()

```go
func (nr *NodoRaft) runElectionTimer() {
    timeoutDuration := nr.electionTimeout()
    nr.Mux.Lock()
    termStarted := nr.CurrentTerm
    nr.Mux.Unlock()
    nr.Logger.Printf("temporizador de elección iniciado (%v), término=%d", 
                     timeoutDuration, termStarted)
```

**Fase 1 - Inicialización**:
1. Calcula un timeout aleatorio
2. Captura el término actual (para detectar cambios)
3. Logea el inicio del timer

```go
ticker := time.NewTicker(10 * time.Millisecond)
def ticker.Stop()

for {
    <-ticker.C
    
    nr.Mux.Lock()
    if nr.EstadoNodo != CANDIDATE && nr.EstadoNodo != FOLLOWER {
        nr.Logger.Printf("salgo del temporizador porque el estado es %s", nr.EstadoNodo)
        nr.Mux.Unlock()
        return
    }
```

**Fase 2 - Loop Principal**:
- Usa un ticker que dispara cada 10ms (chequeo frecuente)
- Si el nodo ya no es CANDIDATE ni FOLLOWER, el timer termina
- **Razón**: Los LEADERS no necesitan timers de elección

```go
    if termStarted != nr.CurrentTerm {
        nr.Logger.Printf("salgo del temporizador porque el término cambió de %d a %d",
                        termStarted, nr.CurrentTerm)
        nr.Mux.Unlock()
        return
    }
```

**Detección de cambio de término**:
- Si el término ha cambiado, significa que otro timer más reciente está corriendo
- Este timer antiguo debe terminar

```go
    if elapsed := time.Since(nr.ElectionResetEvent); elapsed >= timeoutDuration {
        nr.startElection()
        nr.Mux.Unlock()
        return
    }
    nr.Mux.Unlock()
}
```

**Fase 3 - Verificación de Timeout**:
- Calcula el tiempo transcurrido desde el último "reset event"
- Si ha pasado el timeout, inicia una elección
- `ElectionResetEvent` se actualiza cuando:
  - Se recibe un AppendEntries del líder
  - El nodo vota por un candidato
  - El nodo se convierte en candidato

### startElection()

```go
func (nr *NodoRaft) startElection() {
    nr.EstadoNodo = CANDIDATE
    nr.CurrentTerm++
    savedCurrentTerm := nr.CurrentTerm
    nr.ElectionResetEvent = time.Now()
    nr.VotedFor = nr.Yo
    nr.Logger.Printf("es ahora CANDIDATO (término=%d); log=%v",
                     savedCurrentTerm, nr.Log)
    
    votesReceived := 1
```

**Transición a Candidato**:
1. Cambia estado a CANDIDATE
2. Incrementa el término (nuevo período de elección)
3. Resetea el timer de elección
4. Vota por sí mismo
5. Cuenta su propio voto (comienza con 1)

```go
lastLogIndex, lastLogTerm := nr.lastLogIndexAndTerm()

for i := 0; i < len(nr.Nodos); i++ {
    if i == nr.Yo {
        continue
    }
    
    go func(peerId int) {
        args := ArgsPeticionVoto{
            Term:         savedCurrentTerm,
            CandidateId:  nr.Yo,
            LastLogIndex: lastLogIndex,
            LastLogTerm:  lastLogTerm,
        }
```

**Preparación de la Petición**:
- Obtiene índice y término de la última entrada del log
- Para cada nodo (excepto él mismo):
  - Crea una gorutina independiente (RPCs concurrentes)
  - Prepara los argumentos con la información del candidato

**Importancia de LastLogIndex y LastLogTerm**: Raft solo permite que candidatos con logs "al menos tan completos" como los demás puedan ser elegidos (evita pérdida de datos).

```go
        nr.Logger.Printf("enviando PeticionVoto a %d: %+v", peerId, args)
        var reply RespuestaPeticionVoto
        if nr.enviarPeticionVoto(peerId, &args, &reply) {
            nr.Mux.Lock()
            defer nr.Mux.Unlock()
            nr.Logger.Printf("recibida RespuestaPeticionVoto %+v", reply)
            
            if nr.EstadoNodo != CANDIDATE {
                nr.Logger.Printf("mientras esperaba respuesta el estado cambió a %v", nr.EstadoNodo)
                return
            }
```

**Envío de RPC**:
1. Envía RequestVote RPC
2. Si tiene éxito, adquiere el mutex (protección concurrente)
3. Verifica que todavía sea CANDIDATE (podría haber cambiado mientras esperaba la respuesta)

```go
            if reply.Term > savedCurrentTerm {
                nr.Logger.Printf("término desactualizado en RespuestaPeticionVoto")
                nr.becomeFollower(reply.Term)
                return
            } else if reply.Term == savedCurrentTerm {
                if reply.VoteGranted {
                    votesReceived++
                    if votesReceived*2 > len(nr.Nodos) {
                        nr.Logger.Printf("gana la elección con %d votos", votesReceived)
                        nr.startLeader()
                        return
                    }
                }
            }
        }
    }(i)
}
```

**Procesamiento de Respuesta**:
1. **Si reply.Term > savedCurrentTerm**: Otro nodo tiene un término más alto → Convertirse en follower
2. **Si reply.Term == savedCurrentTerm y VoteGranted**:
   - Incrementa contador de votos
   - Si tiene mayoría (más de N/2 votos) → Convertirse en líder
3. **Cálculo de mayoría**: `votesReceived*2 > len(nr.Nodos)` evita problemas con división entera

```go
go nr.runElectionTimer()
```

**Timer de Respaldo**: Inicia un nuevo timer de elección por si esta elección falla (split vote o timeouts).

---

## Transiciones de Estado

### becomeFollower()

```go
func (nr *NodoRaft) becomeFollower(term int) {
    nr.Logger.Printf("es ahora SEGUIDOR con término=%d; log=%v", term, nr.Log)
    nr.EstadoNodo = FOLLOWER
    nr.CurrentTerm = term
    nr.VotedFor = -1
    nr.ElectionResetEvent = time.Now()
    
    go nr.runElectionTimer()
}
```

**Cuándo se llama**:
- Al descubrir un término más alto en cualquier RPC
- Al recibir AppendEntries de un líder válido

**Acciones**:
1. Cambia estado a FOLLOWER
2. Actualiza al nuevo término
3. Resetea el voto (puede votar de nuevo en este término)
4. Resetea el timer de elección
5. Inicia un nuevo timer de elección

**Nota**: No adquiere el mutex porque se espera que el llamador ya lo tenga.

### startLeader()

```go
func (nr *NodoRaft) startLeader() {
    nr.EstadoNodo = LEADER
    nr.IdLider = nr.Yo
    
    for i := 0; i < len(nr.Nodos); i++ {
        if i != nr.Yo {
            nr.NextIndex[i] = len(nr.Log)
            nr.MatchIndex[i] = -1
        }
    }
    nr.Logger.Printf("es ahora LIDER; término=%d, nextIndex=%v, matchIndex=%v; log=%v",
        nr.CurrentTerm, nr.NextIndex, nr.MatchIndex, nr.Log)
```

**Inicialización como Líder**:
1. Cambia estado a LEADER
2. Se marca a sí mismo como líder
3. **Inicializa NextIndex**: Para cada seguidor, asume que su log coincide con el del líder (optimista)
4. **Inicializa MatchIndex**: Para cada seguidor, comienza en -1 (no se ha confirmado ninguna entrada)

**NextIndex vs MatchIndex**:
- **NextIndex**: Siguiente entrada a enviar (especulativo, puede ser incorrecto)
- **MatchIndex**: Última entrada confirmada como replicada (conservador, siempre correcto)

```go
go func() {
    ticker := time.NewTicker(50 * time.Millisecond)
    defer ticker.Stop()
    
    for {
        nr.leaderSendHeartbeats()
        <-ticker.C
        
        nr.Mux.Lock()
        if nr.EstadoNodo != LEADER {
            nr.Mux.Unlock()
            return
        }
        nr.Mux.Unlock()
    }
}()
```

**Gorutina de Heartbeats**:
1. Crea un ticker de 50ms
2. Loop infinito:
   - Envía heartbeats a todos los seguidores
   - Espera 50ms
   - Verifica que todavía sea líder (si no, termina)

**Propósito de los heartbeats**: Evitar que los followers inicien elecciones innecesarias.

---

## Replicación de Log (Líder)

### leaderSendHeartbeats()

```go
func (nr *NodoRaft) leaderSendHeartbeats() {
    nr.Mux.Lock()
    if nr.EstadoNodo != LEADER {
        nr.Mux.Unlock()
        return
    }
    savedCurrentTerm := nr.CurrentTerm
    nr.Mux.Unlock()
```

**Verificación Inicial**: Comprueba que todavía es líder y captura el término actual.

```go
for i := 0; i < len(nr.Nodos); i++ {
    if i == nr.Yo {
        continue
    }
    
    go func(peerId int) {
        nr.Mux.Lock()
        ni := nr.NextIndex[peerId]
        prevLogIndex := ni - 1
        prevLogTerm := -1
        if prevLogIndex >= 0 && prevLogIndex < len(nr.Log) {
            prevLogTerm = nr.Log[prevLogIndex].Term
        }
```

**Preparación de Datos por Seguidor**:
1. `ni` (nextIndex): Próxima entrada a enviar
2. `prevLogIndex`: Índice de la entrada inmediatamente anterior
3. `prevLogTerm`: Término de esa entrada anterior (o -1 si no existe)

**Propósito de prevLogIndex/prevLogTerm**: El seguidor los usa para verificar consistencia del log.

```go
        entries := []EntriesLog{}
        if ni < len(nr.Log) {
            entries = nr.Log[ni:]
        }
        
        args := ArgAppendEntries{
            Term:         savedCurrentTerm,
            LeaderId:     nr.Yo,
            PrevLogIndex: prevLogIndex,
            PrevLogTerm:  prevLogTerm,
            Entries:      entries,
            LeaderCommit: nr.CommitIndex,
        }
        nr.Mux.Unlock()
```

**Construcción del RPC**:
- Si hay entradas nuevas para el seguidor (`ni < len(log)`), las incluye
- Si no hay entradas, envía un heartbeat vacío
- Incluye `LeaderCommit` para que el seguidor actualice su commitIndex

```go
        nr.Logger.Printf("enviando AppendEntries a %v: ni=%d, args=%+v", 
                        peerId, ni, args)
        var reply Results
        if nr.enviarAppendEntries(peerId, &args, &reply) {
            nr.Mux.Lock()
            defer nr.Mux.Unlock()
            
            if reply.Term > nr.CurrentTerm {
                nr.Logger.Printf("término desactualizado en respuesta AppendEntries")
                nr.becomeFollower(reply.Term)
                return
            }
```

**Envío y Verificación Inicial**:
1. Envía AppendEntries RPC
2. Si recibe respuesta, adquiere mutex
3. Si el término del seguidor es mayor, se convierte en follower

```go
            if nr.EstadoNodo == LEADER && savedCurrentTerm == reply.Term {
                if reply.Success {
                    nr.NextIndex[peerId] = ni + len(entries)
                    nr.MatchIndex[peerId] = nr.NextIndex[peerId] - 1
                    nr.Logger.Printf("AppendEntries OK desde %d: nextIndex=%v, matchIndex=%v",
                        peerId, nr.NextIndex, nr.MatchIndex)
```

**Caso de Éxito**:
1. Actualiza `NextIndex[peerId]`: Avanza por todas las entradas enviadas
2. Actualiza `MatchIndex[peerId]`: Confirma que estas entradas están replicadas
3. **Invariante**: `MatchIndex[i] = NextIndex[i] - 1`

```go
                    savedCommitIndex := nr.CommitIndex
                    for i := nr.CommitIndex + 1; i < len(nr.Log); i++ {
                        if nr.Log[i].Term == nr.CurrentTerm {
                            matchCount := 1
                            for peerId := 0; peerId < len(nr.Nodos); peerId++ {
                                if peerId != nr.Yo && nr.MatchIndex[peerId] >= i {
                                    matchCount++
                                }
                            }
                            if matchCount*2 > len(nr.Nodos) {
                                nr.CommitIndex = i
                            }
                        }
                    }
```

**Actualización del CommitIndex** (línea a línea):

1. `for i := nr.CommitIndex + 1`: Itera sobre entradas no comprometidas
2. `if nr.Log[i].Term == nr.CurrentTerm`: **Restricción crucial de Raft**: Solo puede comprometer entradas del término actual
   - **Por qué**: Evita situaciones donde se comprometería una entrada que podría ser sobrescrita
3. `matchCount := 1`: Cuenta el líder mismo
4. `if peerId != nr.Yo && nr.MatchIndex[peerId] >= i`: Cuenta cuántos seguidores tienen esta entrada replicada
5. `if matchCount*2 > len(nr.Nodos)`: Si hay mayoría, comprometer
6. `nr.CommitIndex = i`: Actualiza el índice de compromiso

```go
                    if nr.CommitIndex != savedCommitIndex {
                        nr.Logger.Printf("commitIndex actualizado a %d", nr.CommitIndex)
                        nr.NewCommitReadyChan <- struct{}{}
                    }
```

**Notificación de Nuevas Entradas Comprometidas**: Si el commitIndex avanzó, notifica a `commitChanSender()`.

```go
                } else {
                    nr.NextIndex[peerId] = ni - 1
                    nr.Logger.Printf("AppendEntries falla desde %d: nextIndex ahora %d",
                        peerId, ni-1)
                }
            }
        }
    }(i)
}
```

**Caso de Fallo**:
- Decrementa `NextIndex[peerId]` para reintentar con la entrada anterior
- **Estrategia**: Retrocede uno a uno hasta encontrar el punto de concordancia
- **Optimización posible**: Podría retroceder más rápido usando información adicional

### lastLogIndexAndTerm()

```go
func (nr *NodoRaft) lastLogIndexAndTerm() (int, int) {
    if len(nr.Log) > 0 {
        lastIndex := len(nr.Log) - 1
        return lastIndex, nr.Log[lastIndex].Term
    } else {
        return -1, -1
    }
}
```

**Utilidad**: Devuelve el índice y término de la última entrada del log (o -1, -1 si el log está vacío).

---

## Aplicación de Operaciones

### commitChanSender()

```go
func (nr *NodoRaft) commitChanSender() {
    for range nr.NewCommitReadyChan {
        nr.Mux.Lock()
        savedLastApplied := nr.LastApplied
        var entries []EntriesLog
        if nr.CommitIndex > nr.LastApplied {
            entries = nr.Log[nr.LastApplied+1 : nr.CommitIndex+1]
            nr.LastApplied = nr.CommitIndex
        }
        nr.Mux.Unlock()
```

**Fase 1 - Extracción de Entradas**:
1. Espera notificación en `NewCommitReadyChan`
2. Si hay entradas comprometidas no aplicadas, las extrae
3. Actualiza `LastApplied` para reflejar que se aplicarán

**Por qué usar un canal**: Desacopla la lógica de compromiso (en el líder) de la aplicación (en todos los nodos).

```go
        nr.Logger.Printf("aplicando entradas=%v, último aplicado previo=%d",
                        entries, savedLastApplied)
        
        for i, entry := range entries {
            nr.AplicarOp <- AplicaOperacion{
                Indice:    savedLastApplied + i + 1,
                Operacion: entry.Command,
            }
        }
    }
    nr.Logger.Printf("aplicador de commits finaliza")
}
```

**Fase 2 - Envío al Canal de Aplicación**:
1. Para cada entrada comprometida, crea un `AplicaOperacion`
2. Lo envía al canal `AplicarOp` (donde la máquina de estados lo consumirá)
3. **Invariante**: Las operaciones se aplican en orden estricto de índice

**Terminación**: El loop termina cuando `NewCommitReadyChan` se cierra (en `Para()`).

---

## RPCs del Protocolo Raft

### PedirVoto (RequestVote)

```go
func (nr *NodoRaft) PedirVoto(peticion *ArgsPeticionVoto, 
                              reply *RespuestaPeticionVoto) error {
    nr.Mux.Lock()
    defer nr.Mux.Unlock()
    
    if nr.EstadoNodo == DEAD {
        return nil
    }
```

**Entrada**: Recibe petición de voto de un candidato.

```go
    lastLogIndex, lastLogTerm := nr.lastLogIndexAndTerm()
    nr.Logger.Printf("RequestVote: %+v [currentTerm=%d, votedFor=%d, log index/term=(%d, %d)]",
        peticion, nr.CurrentTerm, nr.VotedFor, lastLogIndex, lastLogTerm)
    
    if peticion.Term > nr.CurrentTerm {
        nr.Logger.Printf("término desactualizado: paso a seguidor")
        nr.becomeFollower(peticion.Term)
    }
```

**Verificación de Término**:
- Si el candidato tiene un término mayor, actualiza el término y se convierte en follower

```go
    if nr.CurrentTerm == peticion.Term &&
        (nr.VotedFor == -1 || nr.VotedFor == peticion.CandidateId) &&
        (peticion.LastLogTerm > lastLogTerm ||
            (peticion.LastLogTerm == lastLogTerm && 
             peticion.LastLogIndex >= lastLogIndex)) {
        reply.VoteGranted = true
        nr.VotedFor = peticion.CandidateId
        nr.ElectionResetEvent = time.Now()
    } else {
        reply.VoteGranted = false
    }
```

**Condiciones para Conceder el Voto** (deben cumplirse TODAS):

1. `nr.CurrentTerm == peticion.Term`: Mismo término
2. `nr.VotedFor == -1 || nr.VotedFor == peticion.CandidateId`: No ha votado aún, o ya votó por este candidato
3. **Restricción de log actualizado**:
   - `peticion.LastLogTerm > lastLogTerm`: El candidato tiene una entrada más reciente, O
   - `peticion.LastLogTerm == lastLogTerm && peticion.LastLogIndex >= lastLogIndex`: Mismo término pero log igual o más largo

**Por qué esta restricción**: Garantiza que solo nodos con logs "completos" pueden ser líderes, evitando pérdida de datos comprometidos.

**Si concede el voto**:
- Marca que votó por este candidato
- Resetea su timer de elección (evita iniciar elección propia)

```go
    reply.Term = nr.CurrentTerm
    nr.Logger.Printf("RespuestaPeticionVoto: %+v", reply)
    return nil
}
```

**Respuesta**: Siempre devuelve el término actual del nodo.

---


### AppendEntries

```go
func (nr *NodoRaft) AppendEntries(args *ArgAppendEntries, 
                                  results *Results) error {
    nr.Mux.Lock()
    defer nr.Mux.Unlock()
    
    if nr.EstadoNodo == DEAD {
        return nil
    }
    
    nr.Logger.Printf("AppendEntries recibido: %+v", args)
```

**Entrada**: Recibe heartbeat o nuevas entradas del líder.

```go
    if args.Term > nr.CurrentTerm {
        nr.Logger.Printf("término desactualizado en AppendEntries")
        nr.becomeFollower(args.Term)
    }
    
    results.Success = false
    if args.Term == nr.CurrentTerm {
        if nr.EstadoNodo != FOLLOWER {
            nr.becomeFollower(args.Term)
        }
        nr.ElectionResetEvent = time.Now()
        nr.IdLider = args.LeaderId
```

**Actualización de Estado**:
1. Si el término del líder es mayor, actualiza y se convierte en follower
2. Si es el término actual:
   - Asegura que está en estado FOLLOWER
   - Resetea el timer de elección (reconoce al líder)
   - Registra quién es el líder

```go
        if args.PrevLogIndex == -1 ||
            (args.PrevLogIndex < len(nr.Log) && 
             args.PrevLogTerm == nr.Log[args.PrevLogIndex].Term) {
            results.Success = true

			// Truncar el log local si es inconsistente y añadir nuevas entradas.
			// Esta es la implementación estándar y robusta.
			nr.Log = append(nr.Log[:args.PrevLogIndex+1], args.Entries...)
			nr.Logger.Printf("log truncado y actualizado. Nuevo log: %v", nr.Log)
```

**Lógica de Replicación (línea a línea)**:

1. **Si la verificación de consistencia es exitosa**:
2. **Operación crítica**: `nr.Log = append(nr.Log[:args.PrevLogIndex+1], args.Entries...)`
   - `nr.Log[:args.PrevLogIndex+1]` crea un slice que contiene la parte del log que se sabe que es consistente.
   - `args.Entries...` desempaqueta las nuevas entradas del líder.
   - `append` combina ambas partes. Si `args.Entries` contiene entradas que solapan con el final del log local, las sobrescribirá. Este es el mecanismo clave de Raft para forzar la consistencia: el log del líder siempre tiene la razón.
3. **Efecto**: El log del seguidor se convierte en una réplica exacta del log del líder hasta el punto de las nuevas entradas.

```go
            if args.LeaderCommit > nr.CommitIndex {
                nr.CommitIndex = min(args.LeaderCommit, len(nr.Log)-1)
                nr.Logger.Printf("actualizando commitIndex a %d", nr.CommitIndex)
                nr.NewCommitReadyChan <- struct{}{}
            }
        }
    }
```

**Actualización del CommitIndex**:
1. Si el líder tiene un commitIndex mayor, actualiza.
2. **Restricción**: `min(args.LeaderCommit, len(nr.Log)-1)` evita comprometer entradas que el seguidor aún no tiene en su log.
3. Notifica a `commitChanSender()` para que aplique las nuevas entradas comprometidas.

```go
    results.Term = nr.CurrentTerm
    nr.Logger.Printf("Respuesta AppendEntries: %+v", *results)
    return nil
}
```

**Respuesta**: Devuelve Success (true/false) y el término actual.

---

## API Público

### Para()

```go
func (nr *NodoRaft) Para() {
    nr.Mux.Lock()
    defer nr.Mux.Unlock()
    if nr.apagado {
        return
    }
    nr.apagado = true
    nr.EstadoNodo = DEAD
    nr.Logger.Printf("es ahora MUERTO")
    close(nr.NewCommitReadyChan)
    close(nr.ShutdownCh)
}
```

**Apagado Ordenado**:
1. Verifica que no esté ya apagado (evita cerrar canales dos veces)
2. Marca el estado como DEAD
3. Cierra canales:
   - `NewCommitReadyChan`: Termina `commitChanSender()`
   - `ShutdownCh`: Señal para otras gorutinas

**Nota**: No espera a que las gorutinas terminen; confía en que verificarán el estado.

### ObtenerEstado()

```go
func (nr *NodoRaft) ObtenerEstado() (int, int, bool, int) {
    nr.Mux.Lock()
    defer nr.Mux.Unlock()
    
    yo := nr.Yo
    mandato := nr.CurrentTerm
    esLider := nr.EstadoNodo == LEADER
    idLider := nr.IdLider
    
    return yo, mandato, esLider, idLider
}
```

**Consulta de Estado**: Devuelve información básica del nodo de forma segura (thread-safe).

### someterOperacion()

```go
func (nr *NodoRaft) someterOperacion(operacion TipoOperacion) (int, int, bool, int, string) {
    nr.Mux.Lock()
    defer nr.Mux.Unlock()
    
    mandato := nr.CurrentTerm
    esLider := nr.EstadoNodo == LEADER
    idLider := nr.IdLider
    indice := -1
    valorADevolver := ""
    
    nr.Logger.Printf("solicitud de usuario recibida en estado %v: %v", nr.EstadoNodo, operacion)
```

**Intento de Someter Operación**: Cliente intenta agregar una nueva operación al log.

```go
    if !esLider {
        valorADevolver = "No es líder"
    } else {
        nr.Log = append(nr.Log, EntriesLog{Command: operacion, Term: nr.CurrentTerm})
        indice = len(nr.Log) - 1
        valorADevolver = "Operación sometida en índice " + fmt.Sprint(indice)
        nr.Logger.Printf("registro actual: %v", nr.Log)
    }
    
    return indice, mandato, esLider, idLider, valorADevolver
}
```

**Lógica**:
1. **Si no es líder**: Rechaza la operación (el cliente debe redirigir al líder)
2. **Si es líder**:
   - Agrega la operación al log con el término actual
   - Devuelve el índice donde se agregó
   - **Importante**: Devolver el índice NO significa que esté comprometida; solo que está en el log local

**Nota sobre consenso**: La operación eventualmente será replicada por los heartbeats periódicos y comprometida cuando haya mayoría.

---

## RPCs de Cliente/API

### ParaNodo()

```go
func (nr *NodoRaft) ParaNodo(args Vacio, reply *Vacio) error {
    nr.Para()
    go func() {
        time.Sleep(20 * time.Millisecond)
        os.Exit(0)
    }()
    return nil
}
```

**RPC para Detener el Nodo**:
1. Llama a `Para()` para apagado limpio
2. Espera 20ms (para que se procesen últimas operaciones)
3. Termina el proceso con `os.Exit(0)`

### ObtenerEstadoNodo()

```go
func (nr *NodoRaft) ObtenerEstadoNodo(args Vacio, reply *EstadoRemoto) error {
    reply.IdNodo, reply.Mandato, reply.EsLider, reply.IdLider = nr.ObtenerEstado()
    return nil
}
```

**RPC para Consultar Estado**: Wrapper RPC para `ObtenerEstado()`.

### SometerOperacionRaft()

```go
func (nr *NodoRaft) SometerOperacionRaft(operacion TipoOperacion, 
                                         reply *ResultadoRemoto) error {
    reply.IndiceRegistro, reply.Mandato, reply.EsLider,
        reply.IdLider, reply.ValorADevolver = nr.someterOperacion(operacion)
    return nil
}
```

**RPC para Someter Operación**: Wrapper RPC para `someterOperacion()`.

---

## Funciones Cliente RPC

### enviarPeticionVoto()

```go
func (nr *NodoRaft) enviarPeticionVoto(nodo int, args *ArgsPeticionVoto,
                                       reply *RespuestaPeticionVoto) bool {
    timeout := 50 * time.Millisecond
    err := nr.Nodos[nodo].CallTimeout("NodoRaft.PedirVoto", args, reply, timeout)
    return err == nil
}
```

**Cliente RPC para RequestVote**:
- Timeout de 50ms (evita bloqueos prolongados)
- Devuelve `true` si la llamada tuvo éxito (no implica que votó a favor)

### enviarAppendEntries()

```go
func (nr *NodoRaft) enviarAppendEntries(nodo int, args *ArgAppendEntries,
                                        results *Results) bool {
    timeout := 50 * time.Millisecond
    err := nr.Nodos[nodo].CallTimeout("NodoRaft.AppendEntries", args, results, timeout)
    return err == nil
}
```

**Cliente RPC para AppendEntries**:
- Timeout de 50ms
- Devuelve `true` si la llamada tuvo éxito (no implica Success=true)

---

## Utilidades

### min()

```go
func min(a, b int) int {
    if a < b {
        return a
    }
    return b
}
```

**Función auxiliar**: Devuelve el mínimo de dos enteros.

---

## Estructuras de Argumentos RPC

### ArgsPeticionVoto

```go
type ArgsPeticionVoto struct {
    Term         int
    CandidateId  int
    LastLogIndex int
    LastLogTerm  int
}
```

**Campos**:
- **Term**: Término del candidato
- **CandidateId**: ID del candidato que solicita el voto
- **LastLogIndex**: Índice de la última entrada del log del candidato
- **LastLogTerm**: Término de la última entrada del log del candidato

### RespuestaPeticionVoto

```go
type RespuestaPeticionVoto struct {
    Term        int
    VoteGranted bool
}
```

**Campos**:
- **Term**: Término actual del votante (para que el candidato actualice el suyo)
- **VoteGranted**: `true` si el votante concedió el voto

### ArgAppendEntries

```go
type ArgAppendEntries struct {
    Term         int
    LeaderId     int
    PrevLogIndex int
    PrevLogTerm  int
    Entries      []EntriesLog
    LeaderCommit int
}
```

**Campos**:
- **Term**: Término del líder
- **LeaderId**: ID del líder (para que los seguidores lo registren)
- **PrevLogIndex**: Índice de la entrada inmediatamente anterior a las nuevas
- **PrevLogTerm**: Término de esa entrada anterior
- **Entries**: Entradas a agregar (vacío para heartbeats)
- **LeaderCommit**: CommitIndex del líder

### Results

```go
type Results struct {
    Term    int
    Success bool
}
```

**Campos**:
- **Term**: Término actual del seguidor
- **Success**: `true` si el seguidor aceptó las entradas

---

## Estructuras de Respuesta API

### EstadoParcial

```go
type EstadoParcial struct {
    Mandato int
    EsLider bool
    IdLider int
}
```

**Información básica del estado de un nodo**.

### EstadoRemoto

```go
type EstadoRemoto struct {
    IdNodo int
    EstadoParcial
}
```

**Respuesta de `ObtenerEstadoNodo()`**: Incluye el ID del nodo consultado.

### ResultadoRemoto

```go
type ResultadoRemoto struct {
    ValorADevolver string
    IndiceRegistro int
    EstadoParcial
}
```

**Respuesta de `SometerOperacionRaft()`**:
- **ValorADevolver**: Mensaje de resultado ("No es líder" o confirmación)
- **IndiceRegistro**: Índice donde se agregó la operación (-1 si fallo)
- Información de estado del nodo

---

## Flujo Completo de una Operación

### Escenario: Cliente somete operación "escribir:x=5"

1. **Cliente → Líder**: `SometerOperacionRaft({Operacion: "escribir", Clave: "x", Valor: "5"})`

2. **Líder**:
   - Verifica que es líder
   - Agrega entrada al log local: `Log = [..., {Term: 3, Command: {escribir, x, 5}}]`
   - Devuelve índice: `indice = 42`

3. **Replicación (próximo heartbeat)**:
   - Líder → Seguidores: `AppendEntries` con la nueva entrada
   - Seguidores verifican consistencia y agregan la entrada
   - Seguidores responden `Success = true`

4. **Compromiso**:
   - Líder cuenta respuestas exitosas
   - Si mayoría (3 de 5), actualiza `CommitIndex = 42`
   - Líder notifica `NewCommitReadyChan`

5. **Aplicación**:
   - `commitChanSender()` detecta nueva entrada comprometida
   - Envía `AplicaOperacion{Indice: 42, Operacion: {escribir, x, 5}}` al canal
   - Máquina de estados recibe y ejecuta: `state["x"] = "5"`

6. **Propagación del Commit**:
   - En próximos heartbeats, líder envía `LeaderCommit = 42`
   - Seguidores actualizan su `CommitIndex` y aplican la operación

**Resultado**: Todos los nodos han aplicado `x=5` de forma consistente.

---

## Garantías del Protocolo Raft

### 1. Election Safety
**Propiedad**: A lo sumo un líder por término.

**Cómo se garantiza**:
- Cada nodo vota solo una vez por término (`VotedFor`)
- Se requiere mayoría para ganar

### 2. Leader Append-Only
**Propiedad**: Los líderes nunca sobrescriben o borran entradas de su log.

**Cómo se garantiza**:
- Los líderes solo agregan (`append`) entradas
- Nunca modifican entradas existentes

### 3. Log Matching
**Propiedad**: Si dos logs contienen una entrada con el mismo índice y término, entonces los logs son idénticos hasta ese índice.

**Cómo se garantiza**:
- `AppendEntries` verifica consistencia con `PrevLogIndex` y `PrevLogTerm`
- Si falla, rechaza y el líder reintenta con entrada anterior

### 4. Leader Completeness
**Propiedad**: Si una entrada se comprometió en un término, estará presente en los logs de todos los líderes futuros.

**Cómo se garantiza**:
- Restricción de voto: Solo candidatos con logs "completos" pueden ganar
- Definición de "completo": `LastLogTerm` mayor o igual, y si igual, `LastLogIndex` mayor o igual

### 5. State Machine Safety
**Propiedad**: Si un servidor ha aplicado una entrada en un índice, ningún otro servidor aplicará una entrada diferente en ese índice.

**Cómo se garantiza**:
- Combinación de todas las propiedades anteriores
- Las entradas solo se aplican después de ser comprometidas

---

## Casos Edge y Consideraciones

### Split Vote (Voto Dividido)

**Problema**: Múltiples candidatos compiten simultáneamente, ninguno obtiene mayoría.

**Solución**: Timeout aleatorio de elección (150-300ms). Eventualmente, un candidato iniciará elección solo y ganará.

### Network Partition (Partición de Red)

**Escenario**: Red se divide en dos grupos: [L, F1, F2] y [F3, F4]

**Comportamiento**:
- Si el líder L está en el grupo mayor, continúa operando
- Si el líder está en el grupo menor, no puede comprometer (no hay mayoría)
- El grupo mayor eventualmente elige nuevo líder

**Resultado**: Cuando la red se cura, el viejo líder se convierte en follower (descubre término mayor).

### Cascading Rollbacks (Retrocesos en Cascada)

**Escenario**: Seguidor está muy retrasado (muchas entradas diferentes).

**Comportamiento actual**: `NextIndex` decrementa de uno en uno.

**Optimización posible**: Seguidor podría devolver el término del conflicto, y líder saltar a la primera entrada de ese término.

### Término Compartido pero Logs Diferentes

**Escenario**: Dos nodos tienen término 5, pero logs diferentes.

**Imposible según Raft**: Si dos nodos tienen una entrada con mismo índice y término, las entradas anteriores deben ser idénticas (Log Matching Property).

**Cómo se previene**: `AppendEntries` verifica consistencia antes de aceptar entradas.

---

## Concurrencia y Sincronización

### Uso del Mutex

**Regla general**: Cualquier acceso a campos compartidos de `NodoRaft` debe estar protegido por `nr.Mux`.

**Campos protegidos**:
- `CurrentTerm`, `VotedFor`, `Log`
- `CommitIndex`, `LastApplied`
- `NextIndex`, `MatchIndex`
- `EstadoNodo`, `IdLider`

**Patrón común**:
```go
nr.Mux.Lock()
// ... leer/modificar estado ...
nr.Mux.Unlock()
```

### Deadlocks Potenciales

**Evitados mediante**:
1. Nunca mantener el mutex mientras se hace RPC
2. Usar `defer nr.Mux.Unlock()` para garantizar liberación
3. No llamar funciones que requieran el mutex mientras ya lo tenemos

**Ejemplo correcto**:
```go
nr.Mux.Lock()
args := crearArgumentos()
nr.Mux.Unlock()
// RPC sin mutex
hacerRPC(args)
```

### Canales y Gorutinas

**Gorutinas principales**:
1. `runElectionTimer()`: Una por ciclo de elección
2. `commitChanSender()`: Una por nodo (toda su vida)
3. `startLeader()` heartbeat loop: Una mientras sea líder
4. `startElection()` vote RPCs: N-1 por elección (donde N = número de nodos)

**Comunicación**:
- `NewCommitReadyChan`: Notificación de nuevas entradas comprometidas
- `ShutdownCh`: Señal de apagado (aunque en el código actual no se usa activamente)
- `AplicarOp`: Canal para enviar operaciones a la máquina de estados

---

## Posibles Mejoras y Extensiones

### 1. Persistencia Real

**Estado actual**: Las variables "persistentes" solo están en memoria.

**Mejora**: Guardar `CurrentTerm`, `VotedFor`, y `Log` en disco antes de responder RPCs.

**Implementación**:
```go
func (nr *NodoRaft) persistState() {
    // Serializar y escribir a disco
    data := serializeState(nr.CurrentTerm, nr.VotedFor, nr.Log)
    writeToFile("state.dat", data)
}
```

### 2. Snapshots

**Problema**: El log crece indefinidamente.

**Solución**: Periódicamente crear snapshots del estado de la máquina de estados y truncar el log.

**Cambios necesarios**:
- Agregar `LastIncludedIndex` y `LastIncludedTerm`
- Nuevo RPC: `InstallSnapshot`
- Ajustar índices para ser relativos al snapshot

### 3. Optimización de AppendEntries Fallidos

**Problema actual**: Retrocede de uno en uno.

**Mejora**: Incluir en `Results`:
```go
type Results struct {
    Term          int
    Success       bool
    ConflictTerm  int  // Término de la entrada conflictiva
    ConflictIndex int  // Primera entrada con ese término
}
```

Líder puede saltar directamente al punto de conflicto.

### 4. Configuración Dinámica

**Estado actual**: El conjunto de nodos es fijo.

**Mejora**: Permitir agregar/remover nodos dinámicamente.

**Desafío**: Evitar que dos mayorías diferentes existan simultáneamente durante la transición.

### 5. Pipeline de AppendEntries

**Estado actual**: Espera respuesta antes de enviar más entradas.

**Mejora**: Enviar múltiples AppendEntries sin esperar respuestas (pipeline).

**Beneficio**: Mayor throughput de replicación.

### 6. Pre-vote

**Problema**: Candidato con red intermitente puede interrumpir líder estable.

**Solución**: Fase de "pre-vote" antes de incrementar término.

**Lógica**: Solo incrementar término si una mayoría responde positivamente a pre-vote.

---

## Debugging y Troubleshooting

### Logs Coloreados

El sistema de logs usa colores para facilitar debugging:

- **Verde**: Eventos de liderazgo (se convierte en líder, commits)
- **Cian**: Eventos de votación (candidatos, RequestVote)
- **Magenta**: RPCs (AppendEntries)
- **Amarillo**: Warnings (términos desactualizados)
- **Rojo**: Errores críticos (nodo muerto, panics)

### Problemas Comunes

#### 1. Elecciones Infinitas

**Síntomas**: Los nodos continúan iniciando elecciones sin llegar a consenso.

**Causas**:
- Timeouts no aleatorios (todos los nodos compiten simultáneamente)
- Red muy lenta (RPCs timeout antes de completarse)

**Debug**: Buscar en logs múltiples "es ahora CANDIDATO" sin "gana la elección".

#### 2. Logs Divergentes

**Síntomas**: Nodos tienen diferentes entradas en el mismo índice.

**Causas**:
- Bug en lógica de AppendEntries (no sobrescribe correctamente)
- Líder no verifica consistencia antes de comprometer

**Debug**: Comparar logs impresos en diferentes nodos.

#### 3. Operaciones No Se Aplican

**Síntomas**: Cliente somete operación, pero nunca se ejecuta.

**Causas**:
- CommitIndex no avanza (no hay mayoría)
- `commitChanSender()` bloqueado o terminado
- Canal `AplicarOp` no se consume

**Debug**: Verificar valores de `CommitIndex` y `LastApplied` en logs.

#### 4. Deadlocks

**Síntomas**: Sistema se congela completamente.

**Causas**:
- Mantener mutex durante RPC
- Esperar canal mientras se tiene mutex

**Debug**: Stack traces de todas las gorutinas (`SIGQUIT` en Unix).

---

## Resumen de Flujos Principales

### Flujo de Elección

1. Follower timeout → `startElection()`
2. Incrementa término, vota por sí mismo
3. Envía RequestVote a todos los peers
4. Peers verifican log y término
5. Si mayoría vota a favor → `startLeader()`
6. Líder inicia heartbeats periódicos

### Flujo de Replicación

1. Cliente → Líder: `SometerOperacion()`
2. Líder agrega al log local
3. Heartbeat: Líder → Seguidores: `AppendEntries`
4. Seguidores verifican consistencia y agregan
5. Seguidores responden Success
6. Líder cuenta respuestas exitosas
7. Si mayoría → Líder actualiza CommitIndex
8. Líder notifica `NewCommitReadyChan`
9. `commitChanSender()` envía a máquina de estados
10. Próximo heartbeat propaga CommitIndex a seguidores

### Flujo de Recuperación de Fallo

1. Nodo S falla y se reinicia (log vacío en esta implementación)
2. Líder intenta AppendEntries
3. S rechaza (PrevLogIndex no coincide)
4. Líder decrementa NextIndex[S]
5. Repite hasta encontrar punto de coincidencia
6. Líder envía todas las entradas desde ese punto
7. S acepta y reconstruye su log

---

## Conclusión

Esta implementación de Raft proporciona las bases del protocolo de consenso:

**Implementado**:
- ✅ Elecciones de líderes
- ✅ Replicación de log
- ✅ Safety properties (con algunas limitaciones)
- ✅ Sistema de logging detallado

**No implementado (necesario para producción)**:
- ❌ Persistencia real en disco
- ❌ Snapshots
- ❌ Configuración dinámica
- ❌ Optimizaciones de rendimiento

**Uso educativo**: Excelente para entender Raft.

**Uso en producción**: Requiere las extensiones mencionadas y pruebas exhaustivas.

---

## Referencias y Lecturas Recomendadas

1. **Paper original de Raft**: "In Search of an Understandable Consensus Algorithm" por Diego Ongaro y John Ousterhout
2. **Tesis de Diego Ongaro**: Explicación detallada con todos los casos edge
3. **Raft Visualization**: https://raft.github.io (visualización interactiva)
4. **Implementaciones de referencia**: 
   - etcd (Go)
   - Consul (Go)
   - LogCabin (C++)
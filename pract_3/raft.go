// Escribir vuestro código de funcionalidad Raft en este fichero
//

package raft

//
// API
// ===
// Este es el API que vuestra implementación debe exportar
//
// nodoRaft = NuevoNodo(...)
//   Crear un nuevo servidor del grupo de elección.
//
// nodoRaft.Para()
//   Solicitar la parado de un servidor
//
// nodo.ObtenerEstado() (yo, mandato, esLider)
//   Solicitar a un nodo de elección por "yo", su mandato en curso,
//   y si piensa que es el msmo el lider
//
// nodoRaft.SometerOperacion(operacion interface()) (indice, mandato, esLider)

// type AplicaOperacion

import (
	"fmt"
	"io"
	"log"
	"os"

	//"crypto/rand"
	"math/rand"
	"sync"
	"time"

	//"net/rpc"

	"raft/internal/comun/rpctimeout"
)

const (
	// Constante para fijar valor entero no inicializado
	IntNOINICIALIZADO = -1

	//  false deshabilita por completo los logs de depuracion
	// Aseguraros de poner kEnableDebugLogs a false antes de la entrega
	kEnableDebugLogs = true

	// Poner a true para logear a stdout en lugar de a fichero
	kLogToStdout = false

	// Cambiar esto para salida de logs en un directorio diferente
	kLogOutputDir = "./logs_raft/"
)

type TipoOperacion struct {
	Operacion string // La operaciones posibles son "leer" y "escribir"
	Clave     string
	Valor     string // en el caso de la lectura Valor = ""
}

// A medida que el nodo Raft conoce las operaciones de las  entradas de registro
// comprometidas, envía un AplicaOperacion, con cada una de ellas, al canal
// "canalAplicar" (funcion NuevoNodo) de la maquina de estados
type AplicaOperacion struct {
	Indice    int // en la entrada de registro
	Operacion TipoOperacion
}

// Tipo de dato Go que representa un solo nodo (réplica) de raft
type NodoRaft struct {
	Mux sync.Mutex // Mutex para proteger acceso a estado compartido
	// Host:Port de todos los nodos (réplicas) Raft, en mismo orden
	Nodos   []rpctimeout.HostPort
	Yo      int // indice de este nodos en campo array "nodos"
	IdLider int
	// Utilización opcional de este logger para depuración
	// Cada nodo Raft tiene su propio registro de trazas (logs)
	Logger *log.Logger

	//Estado Persistente
	CurrentTerm int
	VotedFor    int
	Log         []EntriesLog

	//Estado Volatil
	CommitIndex int //Índice de la última entrada comprometida
	LastApplied int //Índice de la última entrada aplicada a la máquina de estados

	//Volatil para líderes
	NextIndex            []int
	MatchIndex           []int
	VotosObtenidos       int
	OperacionesAplicadas int

	//ESTADO DEL NODO
	EstadoNodo EstadoNodoRaft

	//TIEMPO
	ElectionTimeout  time.Duration //Tiempo de espera para elecciones
	HeartbeatTimeout time.Duration //Tiempo de espera para latidos
	LastHeartbeat    time.Time

	//CANAL
	AplicarOp  chan AplicaOperacion
	ShutdownCh chan struct{} //Canal para terminar gorutinas

}

type EstadoNodoRaft int

const (
	FOLLOWER EstadoNodoRaft = iota
	CANDIDATE
	LEADER
)

type EntriesLog struct {
	Term    int
	Command TipoOperacion
}

// Creacion de un nuevo nodo de eleccion
//
// Tabla de <Direccion IP:puerto> de cada nodo incluido a si mismo.
//
// <Direccion IP:puerto> de este nodo esta en nodos[yo]
//
// Todos los arrays nodos[] de los nodos tienen el mismo orden

// canalAplicar es un canal donde, en la practica 5, se recogerán las
// operaciones a aplicar a la máquina de estados. Se puede asumir que
// este canal se consumira de forma continúa.
//
// NuevoNodo() debe devolver resultado rápido, por lo que se deberían
// poner en marcha Gorutinas para trabajos de larga duracion
func NuevoNodo(nodos []rpctimeout.HostPort, yo int, canalAplicarOperacion chan AplicaOperacion) *NodoRaft {
	nr := &NodoRaft{}
	nr.Nodos = nodos
	nr.Yo = yo
	nr.IdLider = -1

	if kEnableDebugLogs {
		nombreNodo := nodos[yo].Host() + "_" + nodos[yo].Port()
		logPrefix := fmt.Sprintf("%s", nombreNodo)

		fmt.Println("LogPrefix: ", logPrefix)

		if kLogToStdout {
			nr.Logger = log.New(os.Stdout, nombreNodo+" -->> ",
				log.Lmicroseconds|log.Lshortfile)
		} else {
			err := os.MkdirAll(kLogOutputDir, os.ModePerm)
			if err != nil {
				panic(err.Error())
			}
			logOutputFile, err := os.OpenFile(fmt.Sprintf("%s/%s.txt",
				kLogOutputDir, logPrefix), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
			if err != nil {
				panic(err.Error())
			}
			nr.Logger = log.New(logOutputFile,
				logPrefix+" -> ", log.Lmicroseconds|log.Lshortfile)
		}
		nr.Logger.Println("logger initialized")
	} else {
		nr.Logger = log.New(io.Discard, "", 0)
	}
	//Mandato inicial igual a 0, luego va incrementandose
	nr.CurrentTerm = 0
	//VotedFor inicial a -1 (ningun nodo)
	nr.VotedFor = -1

	nr.Log = make([]EntriesLog, 1) //Log empieza en indice 1 ; nr.AplicarOp = canalAplicarOperacion

	nr.CommitIndex = 0
	nr.LastApplied = 0
	nr.NextIndex = make([]int, len(nr.Nodos))
	nr.MatchIndex = make([]int, len(nr.Nodos))
	// La frecuencia de latidos no debe ser superior a 20 veces por segundo
	nr.HeartbeatTimeout = 50 * time.Millisecond
	// El tiempo de espera para elecciones no debe ser superior a 2,5 segundos
	//nr.ElectionTimeout = 2500 * time.Millisecond
	nr.ElectionTimeout = time.Duration(150+rand.Intn(150)) * time.Millisecond // Tiempo entre elecciones aleatorio no superior a 2,5 segundos
	nr.LastHeartbeat = time.Now()                                             //  Inicializar el ultimo heartbeat
	nr.EstadoNodo = FOLLOWER                                                  //Empiezan todos como followers

	//AplicaOperacion := make(chan AplicaOperacion, 100)

	nr.AplicarOp = canalAplicarOperacion
	nr.ShutdownCh = make(chan struct{})

	//GO ROUTINE PENDIENTE
	go nr.gestionEstado() // Iniciamos la goroutina principal
	//go nr.aplicadorDeOperaciones() // Necesario para el Arreglo 3

	return nr
}

// Usamos varios canales en select para poder gestionar tiemouts diversos
func (nr *NodoRaft) gestionEstado() {
	// Crear timer para elecciones (se resetea cada vez que recibimos heartbeat)
	electionTimer := time.NewTimer(nr.ElectionTimeout)
	defer electionTimer.Stop()

	nr.Logger.Println("Gorutina gestionEstado iniciada")

	for {
		select {
		case <-nr.ShutdownCh:
			// Señal de apagado
			nr.Logger.Println("Gorutina gestionEstado terminando")
			return

		case <-electionTimer.C:
			// Timeout de elección: no hemos recibido heartbeat del líder
			nr.Mux.Lock()

			// Solo iniciar elección si NO somos líder
			if nr.EstadoNodo != LEADER {
				nr.Logger.Printf("⏰ Election timeout! Iniciando elección (estado: %v)",
					nr.EstadoNodo)
				nr.iniciarEleccion()
			}

			nr.Mux.Unlock()

			// Resetear timer con nuevo timeout aleatorio
			electionTimer.Reset(time.Duration(150+rand.Intn(150)) * time.Millisecond)

		default:
			// Si somos líder, enviar heartbeats periódicamente
			nr.Mux.Lock()
			esLider := nr.EstadoNodo == LEADER
			nr.Mux.Unlock()

			if esLider {
				nr.enviarHeartbeats()
				time.Sleep(nr.HeartbeatTimeout)
			} else {
				// Si no somos líder, comprobar si necesitamos resetear el timer
				nr.Mux.Lock()
				tiempoDesdeUltimoHeartbeat := time.Since(nr.LastHeartbeat)
				nr.Mux.Unlock()

				if tiempoDesdeUltimoHeartbeat >= nr.ElectionTimeout {
					// Forzar que el timer expire
					electionTimer.Reset(1 * time.Millisecond)
				}

				time.Sleep(10 * time.Millisecond) // Pequeña pausa para no saturar CPU
			}
		}
	}
}

// Funcion que inicia una eleccion en el algoritmo
func (nr *NodoRaft) iniciarEleccion() {
	// IMPORTANTE: Esta función se llama CON EL MUTEX BLOQUEADO

	// Convertirse en candidato
	nr.EstadoNodo = CANDIDATE
	nr.CurrentTerm++      // Incrementar término
	nr.VotedFor = nr.Yo   // Votarse a sí mismo
	nr.VotosObtenidos = 1 // Empezar con nuestro propio voto

	termActual := nr.CurrentTerm

	nr.Logger.Printf("🗳️  CANDIDATO en término %d. Solicitando votos...", termActual)

	// Preparar argumentos para RequestVote
	ultimoIndice := len(nr.Log) - 1
	ultimoTerm := 0
	if ultimoIndice >= 0 {
		ultimoTerm = nr.Log[ultimoIndice].Term
	}

	args := ArgsPeticionVoto{
		Term:         termActual,
		CandidateId:  nr.Yo,
		LastLogIndex: ultimoIndice,
		LastLogTerm:  ultimoTerm,
	}

	// Número de nodos en el cluster
	totalNodos := len(nr.Nodos)
	votosNecesarios := (totalNodos / 2) + 1

	nr.Logger.Printf("Total nodos: %d, Votos necesarios: %d", totalNodos, votosNecesarios)

	// Canal para recibir respuestas de votos
	canalVotos := make(chan bool, totalNodos)

	// Enviar RequestVote a todos los demás nodos (en paralelo)
	for i := 0; i < totalNodos; i++ {
		if i == nr.Yo {
			continue // No enviarnos a nosotros mismos
		}

		// Lanzar gorutina para cada solicitud de voto
		go func(nodo int) {
			reply := RespuestaPeticionVoto{}

			nr.Logger.Printf("Enviando RequestVote a nodo %d", nodo)

			ok := nr.enviarPeticionVoto(nodo, &args, &reply)

			if !ok {
				nr.Logger.Printf("RequestVote a nodo %d falló (timeout o red)", nodo)
				canalVotos <- false
				return
			}

			// Procesar respuesta
			nr.Mux.Lock()
			defer nr.Mux.Unlock()

			// Si descubrimos un término mayor, convertirnos en follower
			if reply.Term > nr.CurrentTerm {
				nr.Logger.Printf("Término mayor detectado (%d > %d). Volviendo a follower",
					reply.Term, nr.CurrentTerm)
				nr.CurrentTerm = reply.Term
				nr.EstadoNodo = FOLLOWER
				nr.VotedFor = -1
				canalVotos <- false
				return
			}

			// Verificar que seguimos siendo candidatos en el mismo término
			if nr.EstadoNodo != CANDIDATE || nr.CurrentTerm != termActual {
				canalVotos <- false
				return
			}

			// Procesar voto
			if reply.VoteGranted {
				nr.Logger.Printf("✓ Voto recibido de nodo %d", nodo)
				canalVotos <- true
			} else {
				nr.Logger.Printf("✗ Voto rechazado por nodo %d", nodo)
				canalVotos <- false
			}
		}(i)
	}

	// Desbloquear el mutex para que las gorutinas puedan trabajar
	nr.Mux.Unlock()

	// Esperar votos con timeout
	timeoutEleccion := time.After(150 * time.Millisecond)
	votosRecibidos := 1 // Ya tenemos nuestro voto
	votosAFavor := 1

	for votosRecibidos < totalNodos {
		select {
		case voto := <-canalVotos:
			votosRecibidos++
			if voto {
				votosAFavor++

				// ¿Ganamos la elección?
				if votosAFavor >= votosNecesarios {
					nr.Mux.Lock()

					// Verificar que seguimos siendo candidatos
					if nr.EstadoNodo == CANDIDATE && nr.CurrentTerm == termActual {
						nr.convertirseEnLider()
					}

					nr.Mux.Unlock()
					return
				}
			}

		case <-timeoutEleccion:
			// Timeout: no conseguimos mayoría a tiempo
			nr.Logger.Printf("⏰ Timeout de elección. Votos: %d/%d",
				votosAFavor, votosNecesarios)
			return
		}
	}

	// Volver a bloquear el mutex (porque gestionEstado espera que esté bloqueado)
	nr.Mux.Lock()
}

// Funcion que envia los heartbeats a los diferentes nodos en el sistema
func (nr *NodoRaft) enviarHeartbeats() {
	nr.Mux.Lock()

	// Verificar que seguimos siendo líder
	if nr.EstadoNodo != LEADER {
		nr.Mux.Unlock()
		return
	}

	termActual := nr.CurrentTerm

	nr.Logger.Printf("💓 Enviando heartbeats (término %d)", termActual)

	// Enviar AppendEntries a cada nodo
	for i := 0; i < len(nr.Nodos); i++ {
		if i == nr.Yo {
			continue // No enviarnos a nosotros mismos
		}

		go func(nodo int) {
			nr.Mux.Lock()

			// Preparar argumentos
			prevLogIndex := nr.NextIndex[nodo] - 1
			prevLogTerm := 0

			if prevLogIndex > 0 && prevLogIndex < len(nr.Log) {
				prevLogTerm = nr.Log[prevLogIndex].Term
			}

			// Determinar qué entradas enviar
			entries := []EntriesLog{}
			if nr.NextIndex[nodo] < len(nr.Log) {
				// Hay entradas por replicar
				entries = nr.Log[nr.NextIndex[nodo]:]
			}

			args := ArgAppendEntries{
				Term:         nr.CurrentTerm,
				LeaderId:     nr.Yo,
				PrevLogIndex: prevLogIndex,
				PrevLogTerm:  prevLogTerm,
				Entries:      entries,
				LeaderCommit: nr.CommitIndex,
			}

			nr.Mux.Unlock()

			// Enviar AppendEntries
			results := Results{}
			ok := nr.enviarAppendEntries(nodo, &args, &results)

			if !ok {
				// Timeout o error de red
				return
			}

			// Procesar respuesta
			nr.Mux.Lock()
			defer nr.Mux.Unlock()

			// Si descubrimos un término mayor, convertirnos en follower
			if results.Term > nr.CurrentTerm {
				nr.Logger.Printf("Término mayor detectado en heartbeat (%d > %d). Dejando de ser líder",
					results.Term, nr.CurrentTerm)
				nr.CurrentTerm = results.Term
				nr.EstadoNodo = FOLLOWER
				nr.VotedFor = -1
				nr.IdLider = -1
				return
			}

			// Verificar que seguimos siendo líder en el mismo término
			if nr.EstadoNodo != LEADER || nr.CurrentTerm != termActual {
				return
			}

			if results.Success {
				// Actualizar nextIndex y matchIndex
				if len(entries) > 0 {
					nr.NextIndex[nodo] = prevLogIndex + len(entries) + 1
					nr.MatchIndex[nodo] = nr.NextIndex[nodo] - 1

					nr.Logger.Printf("Nodo %d actualizado: nextIndex=%d, matchIndex=%d",
						nodo, nr.NextIndex[nodo], nr.MatchIndex[nodo])

					// Intentar avanzar el commitIndex
					nr.actualizarCommitIndex()
				}
			} else {
				// Fallo: decrementar nextIndex y reintentar
				if nr.NextIndex[nodo] > 1 {
					nr.NextIndex[nodo]--
					nr.Logger.Printf("AppendEntries falló para nodo %d. Decrementando nextIndex a %d",
						nodo, nr.NextIndex[nodo])
				}
			}
		}(i)
	}

	nr.Mux.Unlock()
}

func (nr *NodoRaft) actualizarCommitIndex() {

	// Solo el líder puede avanzar commitIndex
	if nr.EstadoNodo != LEADER {
		return
	}

	// Buscar el índice N tal que:
	// - N > commitIndex
	// - Mayoría de matchIndex[i] >= N
	// - log[N].term == currentTerm

	for n := len(nr.Log) - 1; n > nr.CommitIndex; n-- {
		// Verificar que la entrada sea del término actual
		if nr.Log[n].Term != nr.CurrentTerm {
			continue
		}

		// Contar cuántos nodos tienen esta entrada
		replicasConEntrada := 1 // El líder siempre la tiene

		for i := 0; i < len(nr.Nodos); i++ {
			if i == nr.Yo {
				continue
			}

			if nr.MatchIndex[i] >= n {
				replicasConEntrada++
			}
		}

		// ¿Tenemos mayoría?
		mayoria := (len(nr.Nodos) / 2) + 1
		if replicasConEntrada >= mayoria {
			nr.Logger.Printf("✓ Commit avanzado de %d a %d (réplicas: %d/%d)",
				nr.CommitIndex, n, replicasConEntrada, len(nr.Nodos))
			nr.CommitIndex = n
			break
		}
	}
}

// Funcion que transforma al nodo en lider
func (nr *NodoRaft) convertirseEnLider() {
	// IMPORTANTE: Esta función se llama CON EL MUTEX BLOQUEADO

	nr.EstadoNodo = LEADER
	nr.IdLider = nr.Yo

	nr.Logger.Printf("👑 ¡SOY EL LÍDER en término %d!", nr.CurrentTerm)

	// Inicializar nextIndex y matchIndex
	for i := range nr.NextIndex {
		nr.NextIndex[i] = len(nr.Log) // Optimista: asumimos que todos están actualizados
		nr.MatchIndex[i] = 0
	}

	// Enviar heartbeat inmediato para establecer autoridad
	go nr.enviarHeartbeats()
}

// Metodo Para() utilizado cuando no se necesita mas al nodo
//
// Quizas interesante desactivar la salida de depuracion
// de este nodo
func (nr *NodoRaft) Para() {
	close(nr.ShutdownCh)
	go func() { time.Sleep(5 * time.Millisecond); os.Exit(0) }()
}

// Devuelve "yo", mandato en curso y si este nodo cree ser lider
//
// Primer valor devuelto es el indice de este  nodo Raft el el conjunto de nodos
// la operacion si consigue comprometerse.
// El segundo valor es el mandato en curso
// El tercer valor es true si el nodo cree ser el lider
// Cuarto valor es el lider, es el indice del líder si no es él
func (nr *NodoRaft) obtenerEstado() (int, int, bool, int) {
	var yo int = nr.Yo
	var mandato int
	var esLider bool
	var idLider int = nr.IdLider

	// Vuestro codigo aqui
	nr.Mux.Lock() //Aseguramos exclusión mutua al leer el estado
	mandato = nr.CurrentTerm
	if nr.EstadoNodo == LEADER {
		esLider = true
	}
	nr.Mux.Unlock()

	return yo, mandato, esLider, idLider
}

// El servicio que utilice Raft (base de datos clave/valor, por ejemplo)
// Quiere buscar un acuerdo de posicion en registro para siguiente operacion
// solicitada por cliente.

// Si el nodo no es el lider, devolver falso
// Sino, comenzar la operacion de consenso sobre la operacion y devolver en
// cuanto se consiga
//
// No hay garantia que esta operacion consiga comprometerse en una entrada de
// de registro, dado que el lider puede fallar y la entrada ser reemplazada
// en el futuro.
// Primer valor devuelto es el indice del registro donde se va a colocar
// la operacion si consigue comprometerse.
// El segundo valor es el mandato en curso
// El tercer valor es true si el nodo cree ser el lider
// Cuarto valor es el lider, es el indice del líder si no es él
func (nr *NodoRaft) someterOperacion(operacion TipoOperacion) (int, int,
	bool, int, string) {
	indice := -1
	mandato := -1
	EsLider := false
	idLider := -1
	valorADevolver := ""
	indice, mandato, EsLider, idLider = nr.obtenerEstado()
	if !EsLider {
		valorADevolver = "No es líder"
	} else {
		nr.Mux.Lock()
		//Añadir la operación al log
		newEntry := EntriesLog{
			Term:    nr.CurrentTerm,
			Command: operacion,
		}
		nr.Log = append(nr.Log, newEntry)
		indice = len(nr.Log) - 1 //Índice de la nueva entrada
		nr.Mux.Unlock()
		valorADevolver = "Operación sometida en índice " + fmt.Sprint(indice)

	}

	return indice, mandato, EsLider, idLider, valorADevolver
}

// -----------------------------------------------------------------------
// LLAMADAS RPC al API
//
// Si no tenemos argumentos o respuesta estructura vacia (tamaño cero)
type Vacio struct{}

func (nr *NodoRaft) ParaNodo(args Vacio, reply *Vacio) error {
	defer nr.Para()
	return nil
}

type EstadoParcial struct {
	Mandato int
	EsLider bool
	IdLider int
}

type EstadoRemoto struct {
	IdNodo int
	EstadoParcial
}

func (nr *NodoRaft) ObtenerEstadoNodo(args Vacio, reply *EstadoRemoto) error {
	reply.IdNodo, reply.Mandato, reply.EsLider, reply.IdLider = nr.obtenerEstado()
	return nil
}

type ResultadoRemoto struct {
	ValorADevolver string
	IndiceRegistro int
	EstadoParcial
}

func (nr *NodoRaft) SometerOperacionRaft(operacion TipoOperacion,
	reply *ResultadoRemoto) error {
	reply.IndiceRegistro, reply.Mandato, reply.EsLider,
		reply.IdLider, reply.ValorADevolver = nr.someterOperacion(operacion)
	return nil
}

// -----------------------------------------------------------------------
// LLAMADAS RPC protocolo RAFT
//
// Structura de ejemplo de argumentos de RPC PedirVoto.
//
// Recordar
// -----------
// Nombres de campos deben comenzar con letra mayuscula !
type ArgsPeticionVoto struct {
	Term         int // Término del candidato
	CandidateId  int // ID del candidato
	LastLogIndex int // Índice de la última entrada del log
	LastLogTerm  int // Término de la última entrada del log
}

// Structura de ejemplo de respuesta de RPC PedirVoto,
//
// Recordar
// -----------
// Nombres de campos deben comenzar con letra mayuscula !
type RespuestaPeticionVoto struct {
	Term        int  // Término actual del votante
	VoteGranted bool // true si concede el voto
}

// Metodo para RPC PedirVoto
func (nr *NodoRaft) PedirVoto(peticion *ArgsPeticionVoto,
	reply *RespuestaPeticionVoto) error {
	nr.Mux.Lock()
	defer nr.Mux.Unlock()

	nr.Logger.Printf("PedirVoto recibido de candidato %d para término %d (mi término: %d)",
		peticion.CandidateId, peticion.Term, nr.CurrentTerm)

	// Inicializar respuesta con valores por defecto
	reply.Term = nr.CurrentTerm
	reply.VoteGranted = false

	// Regla 1: Si el término del candidato es menor, rechazar
	if peticion.Term < nr.CurrentTerm {
		nr.Logger.Printf("Voto rechazado: término obsoleto (%d < %d)",
			peticion.Term, nr.CurrentTerm)
		return nil
	}

	// Regla 2: Si el término del candidato es mayor, actualizarse
	if peticion.Term > nr.CurrentTerm {
		nr.Logger.Printf("Término mayor detectado. Actualizando de %d a %d",
			nr.CurrentTerm, peticion.Term)
		nr.CurrentTerm = peticion.Term
		nr.EstadoNodo = FOLLOWER
		nr.VotedFor = -1 // Resetear voto para el nuevo término
		reply.Term = nr.CurrentTerm
	}

	// Regla 3: Verificar si ya votamos en este término
	yaVotePorOtro := (nr.VotedFor != -1 && nr.VotedFor != peticion.CandidateId)
	if yaVotePorOtro {
		nr.Logger.Printf("Voto rechazado: ya voté por %d en término %d",
			nr.VotedFor, nr.CurrentTerm)
		return nil
	}

	// Regla 4: Verificar que el log del candidato esté actualizado
	// El log del candidato está actualizado si:
	// - Su último término es mayor, O
	// - Su último término es igual Y su log es al menos tan largo
	miUltimoIndice := len(nr.Log) - 1
	miUltimoTerm := 0
	if miUltimoIndice >= 0 {
		miUltimoTerm = nr.Log[miUltimoIndice].Term
	}

	logActualizado := false
	if peticion.LastLogTerm > miUltimoTerm {
		logActualizado = true
	} else if peticion.LastLogTerm == miUltimoTerm {
		if peticion.LastLogIndex >= miUltimoIndice {
			logActualizado = true
		}
	}

	if !logActualizado {
		nr.Logger.Printf("Voto rechazado: log no actualizado. Candidato: (idx:%d,term:%d), Yo: (idx:%d,term:%d)",
			peticion.LastLogIndex, peticion.LastLogTerm, miUltimoIndice, miUltimoTerm)
		return nil
	}

	// ¡Conceder el voto!
	nr.VotedFor = peticion.CandidateId
	reply.VoteGranted = true
	nr.LastHeartbeat = time.Now() // Resetear timeout de elección

	nr.Logger.Printf("✓ Voto concedido a candidato %d en término %d",
		peticion.CandidateId, nr.CurrentTerm)

	return nil
}

type ArgAppendEntries struct {
	Term         int          // Término del líder
	LeaderId     int          // ID del líder
	PrevLogIndex int          // Índice de la entrada previa
	PrevLogTerm  int          // Término de la entrada previa
	Entries      []EntriesLog // Entradas a replicar (vacío para heartbeat)
	LeaderCommit int          // Índice de commit del líder
}

type Results struct {
	Term    int  // Término actual del seguidor
	Success bool // true si el seguidor tiene la entrada en PrevLogIndex
}

// Metodo de tratamiento de llamadas RPC AppendEntries
func (nr *NodoRaft) AppendEntries(args *ArgAppendEntries,
	results *Results) error {

	nr.Mux.Lock()
	defer nr.Mux.Unlock()

	// Log para debugging
	if len(args.Entries) == 0 {
		nr.Logger.Printf("Heartbeat recibido del líder %d (término %d)",
			args.LeaderId, args.Term)
	} else {
		nr.Logger.Printf("AppendEntries recibido del líder %d: %d entradas",
			args.LeaderId, len(args.Entries))
	}

	// Inicializar respuesta
	results.Term = nr.CurrentTerm
	results.Success = false

	// Regla 1: Rechazar si el término es menor que el actual
	if args.Term < nr.CurrentTerm {
		nr.Logger.Printf("AppendEntries rechazado: término obsoleto (%d < %d)",
			args.Term, nr.CurrentTerm)
		return nil
	}

	// Regla 2: Si el término es mayor o igual, actualizar estado
	if args.Term >= nr.CurrentTerm {
		nr.CurrentTerm = args.Term
		results.Term = nr.CurrentTerm

		// Si no era follower, convertirse
		if nr.EstadoNodo != FOLLOWER {
			nr.Logger.Printf("Convirtiéndose en follower (término %d)", nr.CurrentTerm)
			nr.EstadoNodo = FOLLOWER
		}

		nr.IdLider = args.LeaderId
		nr.VotedFor = -1 // Resetear voto
	}

	// Importante: Resetear timeout de elección (esto evita nuevas elecciones)
	nr.LastHeartbeat = time.Now()

	// Regla 3: Verificar consistencia del log
	// El líder nos dice: "antes de estas entradas, deberías tener entrada en PrevLogIndex con PrevLogTerm"
	if args.PrevLogIndex > 0 {
		// Verificar que tengamos esa entrada
		if args.PrevLogIndex >= len(nr.Log) {
			nr.Logger.Printf("AppendEntries rechazado: log muy corto (PrevLogIndex:%d >= len:%d)",
				args.PrevLogIndex, len(nr.Log))
			return nil
		}

		// Verificar que el término coincida
		if nr.Log[args.PrevLogIndex].Term != args.PrevLogTerm {
			nr.Logger.Printf("AppendEntries rechazado: término no coincide en idx %d (tengo:%d, esperado:%d)",
				args.PrevLogIndex, nr.Log[args.PrevLogIndex].Term, args.PrevLogTerm)
			return nil
		}
	}

	// Regla 4: Si es un heartbeat vacío, solo actualizar commit
	if len(args.Entries) == 0 {
		results.Success = true

		// Actualizar commitIndex si el líder está más avanzado
		if args.LeaderCommit > nr.CommitIndex {
			// Commitear hasta donde el líder dice, pero no más allá de nuestro log
			nr.CommitIndex = min(args.LeaderCommit, len(nr.Log)-1)
			nr.Logger.Printf("CommitIndex actualizado a %d", nr.CommitIndex)
		}

		return nil
	}

	// Regla 5: Añadir nuevas entradas
	// Eliminar entradas conflictivas y añadir las nuevas
	indiceInsercion := args.PrevLogIndex + 1

	for i, entrada := range args.Entries {
		indiceActual := indiceInsercion + i

		// Si ya existe una entrada en esta posición
		if indiceActual < len(nr.Log) {
			// Si hay conflicto de términos, eliminar esta y todas las siguientes
			if nr.Log[indiceActual].Term != entrada.Term {
				nr.Logger.Printf("Conflicto detectado en índice %d. Truncando log.", indiceActual)
				nr.Log = nr.Log[:indiceActual]
				nr.Log = append(nr.Log, entrada)
			}
			// Si coinciden, no hacer nada (ya la tenemos)
		} else {
			// Añadir nueva entrada
			nr.Log = append(nr.Log, entrada)
		}
	}

	nr.Logger.Printf("Log actualizado. Longitud: %d", len(nr.Log))

	// Regla 6: Actualizar commitIndex
	if args.LeaderCommit > nr.CommitIndex {
		nr.CommitIndex = min(args.LeaderCommit, len(nr.Log)-1)
		nr.Logger.Printf("CommitIndex actualizado a %d", nr.CommitIndex)
	}

	results.Success = true
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ----- Metodos/Funciones a utilizar como clientes
//
//

// Ejemplo de código enviarPeticionVoto
//
// nodo int -- indice del servidor destino en nr.nodos[]
//
// args *RequestVoteArgs -- argumentos par la llamada RPC
//
// reply *RequestVoteReply -- respuesta RPC
//
// Los tipos de argumentos y respuesta pasados a CallTimeout deben ser
// los mismos que los argumentos declarados en el metodo de tratamiento
// de la llamada (incluido si son punteros
//
// Si en la llamada RPC, la respuesta llega en un intervalo de tiempo,
// la funcion devuelve true, sino devuelve false
//
// la llamada RPC deberia tener un timout adecuado.
//
// Un resultado falso podria ser causado por una replica caida,
// un servidor vivo que no es alcanzable (por problemas de red ?),
// una petición perdida, o una respuesta perdida
//
// Para problemas con funcionamiento de RPC, comprobar que la primera letra
// del nombre  todo los campos de la estructura (y sus subestructuras)
// pasadas como parametros en las llamadas RPC es una mayuscula,
// Y que la estructura de recuperacion de resultado sea un puntero a estructura
// y no la estructura misma.
func (nr *NodoRaft) enviarPeticionVoto(nodo int, args *ArgsPeticionVoto, reply *RespuestaPeticionVoto) bool {

	timeout := 50 * time.Millisecond
	err := nr.Nodos[nodo].CallTimeout("NodoRaft.PedirVoto", args, reply, timeout)
	return err == nil

}

func (nr *NodoRaft) enviarAppendEntries(nodo int, args *ArgAppendEntries, results *Results) bool {

	timeout := 50 * time.Millisecond

	err := nr.Nodos[nodo].CallTimeout("NodoRaft.AppendEntries",
		args, results, timeout)

	return err == nil
}

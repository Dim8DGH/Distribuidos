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
// nodoRaft.SometerOperacion(operacion interface{}) (indice, mandato, esLider)

// type AplicaOperacion

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"math/rand"
	"sync"
	"time"

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

	ansiReset   = "\033[0m"
	ansiInfo    = "\033[37m"
	ansiLeader  = "\033[32m"
	ansiVote    = "\033[36m"
	ansiRPC     = "\033[35m"
	ansiWarning = "\033[33m"
	ansiError   = "\033[31m"
)

type colorWriter struct {
	writer io.Writer
}

func (cw colorWriter) Write(p []byte) (int, error) {
	colored := applyLogColor(string(p))
	return cw.writer.Write([]byte(colored))
}

func applyLogColor(s string) string {
	text := strings.TrimSuffix(s, "\n")
	color := ansiInfo

	switch {
	case strings.Contains(text, "es ahora LIDER"),
		strings.Contains(text, "commitIndex actualizado"),
		strings.Contains(text, "gana la elección"),
		strings.Contains(text, "AppendEntries OK"),
		strings.Contains(text, "aplicando entradas"):
		color = ansiLeader
	case strings.Contains(text, "es ahora CANDIDATO"),
		strings.Contains(text, "temporizador de elección"),
		strings.Contains(text, "PeticiónVoto"):
		color = ansiVote
	case strings.Contains(text, "AppendEntries"):
		color = ansiRPC
	case strings.Contains(text, "término desactualizado") || strings.Contains(text, "AppendEntries falla") ||
		strings.Contains(text, "se vuelve seguidor"):
		color = ansiWarning
	case strings.Contains(text, "es ahora MUERTO") || strings.Contains(text, "panic"):
		color = ansiError
	}

	colored := color + text + ansiReset
	if strings.HasSuffix(s, "\n") {
		colored += "\n"
	}
	return colored
}

// Tipo de dato Go que representa una operación
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
	CommitIndex int // Índice de la última entrada comprometida
	LastApplied int // Índice de la última entrada aplicada a la máquina de estados

	//Volatil para líderes
	NextIndex  map[int]int
	MatchIndex map[int]int

	//ESTADO DEL NODO
	EstadoNodo EstadoNodoRaft

	//TIEMPO
	ElectionResetEvent time.Time

	//CANAL
	AplicarOp          chan AplicaOperacion
	NewCommitReadyChan chan struct{}
	ShutdownCh         chan struct{} //Canal para terminar gorutinas
	apagado            bool
}

type EstadoNodoRaft int

const (
	FOLLOWER EstadoNodoRaft = iota
	CANDIDATE
	LEADER
	DEAD
)

func (s EstadoNodoRaft) String() string {
	switch s {
	case FOLLOWER:
		return "Follower"
	case CANDIDATE:
		return "Candidate"
	case LEADER:
		return "Leader"
	case DEAD:
		return "Dead"
	default:
		panic("unreachable")
	}
}

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
			coloredStdout := colorWriter{writer: os.Stdout}
			nr.Logger = log.New(coloredStdout, nombreNodo+" -->> ",
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
			coloredFile := colorWriter{writer: logOutputFile}
			nr.Logger = log.New(coloredFile,
				logPrefix+" -> ", log.Lmicroseconds|log.Lshortfile)
		}
		nr.Logger.Println("logger initialized")
	} else {
		nr.Logger = log.New(io.Discard, "", 0)
	}

	// Estado persistente inicial
	nr.CurrentTerm = 0
	nr.VotedFor = -1
	nr.Log = make([]EntriesLog, 0) // Log comienza vacío

	// Estado volátil inicial
	nr.CommitIndex = -1
	nr.LastApplied = -1
	nr.NextIndex = make(map[int]int)
	nr.MatchIndex = make(map[int]int)

	// Estado del nodo
	nr.EstadoNodo = FOLLOWER
	nr.ElectionResetEvent = time.Now()

	// Canales
	nr.AplicarOp = canalAplicarOperacion
	nr.NewCommitReadyChan = make(chan struct{}, 16)
	nr.ShutdownCh = make(chan struct{})
	nr.apagado = false

	// Iniciar gorutinas
	go nr.runElectionTimer()

	go nr.commitChanSender()

	return nr
}

// electionTimeout genera un timeout de elección pseudo-aleatorio
func (nr *NodoRaft) electionTimeout() time.Duration {
	return time.Duration(150+rand.Intn(150)) * time.Millisecond
}

// runElectionTimer implementa el timer de elección
// Se ejecuta en una gorutina separada y es bloqueante
func (nr *NodoRaft) runElectionTimer() {
	timeoutDuration := nr.electionTimeout()
	nr.Mux.Lock()
	termStarted := nr.CurrentTerm
	nr.Mux.Unlock()
	nr.Logger.Printf("temporizador de elección iniciado (%v), término=%d", timeoutDuration, termStarted)

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		<-ticker.C

		nr.Mux.Lock()
		if nr.EstadoNodo != CANDIDATE && nr.EstadoNodo != FOLLOWER {
			nr.Logger.Printf("salgo del temporizador porque el estado es %s", nr.EstadoNodo)
			nr.Mux.Unlock()
			return
		}

		if termStarted != nr.CurrentTerm {
			nr.Logger.Printf("salgo del temporizador porque el término cambió de %d a %d", termStarted, nr.CurrentTerm)
			nr.Mux.Unlock()
			return
		}

		// Iniciar elección si no hemos recibido heartbeat en el timeout
		if elapsed := time.Since(nr.ElectionResetEvent); elapsed >= timeoutDuration {
			nr.startElection()
			nr.Mux.Unlock()
			return
		}
		nr.Mux.Unlock()
	}
}

// startElection inicia una nueva elección con este nodo como candidato
// Espera que nr.Mux esté bloqueado
func (nr *NodoRaft) startElection() {
	nr.EstadoNodo = CANDIDATE
	nr.CurrentTerm++
	savedCurrentTerm := nr.CurrentTerm
	nr.ElectionResetEvent = time.Now()
	nr.VotedFor = nr.Yo
	nr.Logger.Printf("es ahora CANDIDATO (término=%d); log=%v", savedCurrentTerm, nr.Log)

	votesReceived := 1

	// Obtener índice y término del último log
	lastLogIndex, lastLogTerm := nr.lastLogIndexAndTerm()

	// Enviar RequestVote RPCs a todos los demás servidores concurrentemente
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

				if reply.Term > savedCurrentTerm {
					nr.Logger.Printf("término desactualizado en RespuestaPeticionVoto")
					nr.becomeFollower(reply.Term)
					return
				} else if reply.Term == savedCurrentTerm {
					if reply.VoteGranted {
						votesReceived++
						if votesReceived*2 > len(nr.Nodos) {
							// ¡Ganamos la elección!
							nr.Logger.Printf("gana la elección con %d votos", votesReceived)
							nr.startLeader()
							return
						}
					}
				}
			}
		}(i)
	}

	// Ejecutar otro timer de elección, en caso de que esta elección no tenga éxito
	go nr.runElectionTimer()
}

// becomeFollower convierte al nodo en follower y resetea su estado
// Espera que nr.Mux esté bloqueado
func (nr *NodoRaft) becomeFollower(term int) {
	nr.Logger.Printf("es ahora SEGUIDOR con término=%d; log=%v", term, nr.Log)
	nr.EstadoNodo = FOLLOWER
	nr.CurrentTerm = term
	nr.VotedFor = -1
	nr.ElectionResetEvent = time.Now()

	go nr.runElectionTimer()
}

// startLeader convierte al nodo en líder y comienza el proceso de heartbeats
// Espera que nr.Mux esté bloqueado
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

	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()

		// Enviar heartbeats periódicos mientras sigamos siendo líder
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
}

// leaderSendHeartbeats envía una ronda de heartbeats a todos los peers

func (nr *NodoRaft) leaderSendHeartbeats() {
	nr.Mux.Lock()
	if nr.EstadoNodo != LEADER {
		nr.Mux.Unlock()
		return
	}
	savedCurrentTerm := nr.CurrentTerm
	nr.Mux.Unlock()

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

			nr.Logger.Printf("enviando AppendEntries a %v: ni=%d, args=%+v", peerId, ni, args)
			var reply Results
			if nr.enviarAppendEntries(peerId, &args, &reply) {
				nr.Mux.Lock()
				defer nr.Mux.Unlock()

				if reply.Term > nr.CurrentTerm {
					nr.Logger.Printf("término desactualizado en respuesta AppendEntries")
					nr.becomeFollower(reply.Term)
					return
				}

				if nr.EstadoNodo == LEADER && savedCurrentTerm == reply.Term {
					if reply.Success {
						nr.NextIndex[peerId] = ni + len(entries)
						nr.MatchIndex[peerId] = nr.NextIndex[peerId] - 1
						nr.Logger.Printf("AppendEntries OK desde %d: nextIndex=%v, matchIndex=%v",
							peerId, nr.NextIndex, nr.MatchIndex)

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
						if nr.CommitIndex != savedCommitIndex {
							nr.Logger.Printf("commitIndex actualizado a %d", nr.CommitIndex)
							nr.NewCommitReadyChan <- struct{}{}
						}
					} else {
						nr.NextIndex[peerId] = ni - 1
						nr.Logger.Printf("AppendEntries falla desde %d: nextIndex ahora %d", peerId, ni-1)
					}
				}
			}
		}(i)
	}
}

// lastLogIndexAndTerm devuelve el último índice del log y el término de la última entrada
// (o -1 si no hay log)
// Espera que nr.Mux esté bloqueado
func (nr *NodoRaft) lastLogIndexAndTerm() (int, int) {
	if len(nr.Log) > 0 {
		lastIndex := len(nr.Log) - 1
		return lastIndex, nr.Log[lastIndex].Term
	} else {
		return -1, -1
	}
}

// commitChanSender es responsable de enviar entradas comprometidas en nr.AplicarOp
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
		nr.Logger.Printf("aplicando entradas=%v, último aplicado previo=%d", entries, savedLastApplied)

		for i, entry := range entries {
			nr.AplicarOp <- AplicaOperacion{
				Indice:    savedLastApplied + i + 1,
				Operacion: entry.Command,
			}
		}
	}
	nr.Logger.Printf("aplicador de commits finaliza")
}

// Metodo Para() utilizado cuando no se necesita mas al nodo
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

// Devuelve "yo", mandato en curso y si este nodo cree ser lider
func (nr *NodoRaft) ObtenerEstado() (int, int, bool, int) {
	nr.Mux.Lock()
	defer nr.Mux.Unlock()

	yo := nr.Yo
	mandato := nr.CurrentTerm
	esLider := nr.EstadoNodo == LEADER
	idLider := nr.IdLider

	return yo, mandato, esLider, idLider
}

// someterOperacion intenta someter una operación al log, habria que esperar a que haya sido replicado una mayoria de veces
func (nr *NodoRaft) someterOperacion(operacion TipoOperacion) (int, int, bool, int, string) {
	nr.Mux.Lock()
	defer nr.Mux.Unlock()

	mandato := nr.CurrentTerm
	esLider := nr.EstadoNodo == LEADER
	idLider := nr.IdLider
	indice := -1
	valorADevolver := ""

	nr.Logger.Printf("solicitud de usuario recibida en estado %v: %v", nr.EstadoNodo, operacion)

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

// -----------------------------------------------------------------------
// LLAMADAS RPC al API
type Vacio struct{}

func (nr *NodoRaft) ParaNodo(args Vacio, reply *Vacio) error {
	nr.Para()
	go func() {
		time.Sleep(20 * time.Millisecond)
		os.Exit(0)
	}()
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
	reply.IdNodo, reply.Mandato, reply.EsLider, reply.IdLider = nr.ObtenerEstado()
	return nil
}

type ResultadoRemoto struct {
	ValorADevolver string
	IndiceRegistro int
	EstadoParcial
}

func (nr *NodoRaft) SometerOperacionRaft(operacion TipoOperacion, reply *ResultadoRemoto) error {
	reply.IndiceRegistro, reply.Mandato, reply.EsLider,
		reply.IdLider, reply.ValorADevolver = nr.someterOperacion(operacion)
	return nil
}

// -----------------------------------------------------------------------
// LLAMADAS RPC protocolo RAFT
type ArgsPeticionVoto struct {
	Term         int
	CandidateId  int
	LastLogIndex int
	LastLogTerm  int
}

type RespuestaPeticionVoto struct {
	Term        int
	VoteGranted bool
}

// Metodo para RPC PedirVoto
func (nr *NodoRaft) PedirVoto(peticion *ArgsPeticionVoto, reply *RespuestaPeticionVoto) error {
	nr.Mux.Lock()
	defer nr.Mux.Unlock()

	if nr.EstadoNodo == DEAD {
		return nil
	}

	lastLogIndex, lastLogTerm := nr.lastLogIndexAndTerm()
	nr.Logger.Printf("RequestVote: %+v [currentTerm=%d, votedFor=%d, log index/term=(%d, %d)]",
		peticion, nr.CurrentTerm, nr.VotedFor, lastLogIndex, lastLogTerm)

	if peticion.Term > nr.CurrentTerm {
		nr.Logger.Printf("término desactualizado: paso a seguidor")
		nr.becomeFollower(peticion.Term)
	}

	if nr.CurrentTerm == peticion.Term &&
		(nr.VotedFor == -1 || nr.VotedFor == peticion.CandidateId) &&
		(peticion.LastLogTerm > lastLogTerm ||
			(peticion.LastLogTerm == lastLogTerm && peticion.LastLogIndex >= lastLogIndex)) {
		reply.VoteGranted = true
		nr.VotedFor = peticion.CandidateId
		nr.ElectionResetEvent = time.Now()
	} else {
		reply.VoteGranted = false
	}

	reply.Term = nr.CurrentTerm
	nr.Logger.Printf("RespuestaPeticionVoto: %+v", reply)
	return nil
}

type ArgAppendEntries struct {
	Term         int
	LeaderId     int
	PrevLogIndex int
	PrevLogTerm  int
	Entries      []EntriesLog
	LeaderCommit int
}

type Results struct {
	Term    int
	Success bool
}

// Metodo de tratamiento de llamadas RPC AppendEntries
func (nr *NodoRaft) AppendEntries(args *ArgAppendEntries, results *Results) error {
	nr.Mux.Lock()
	defer nr.Mux.Unlock()

	if nr.EstadoNodo == DEAD {
		return nil
	}

	nr.Logger.Printf("AppendEntries recibido: %+v", args)

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

		// Verificar si nuestro log contiene una entrada en PrevLogIndex cuyo término coincide
		if args.PrevLogIndex == -1 ||
			(args.PrevLogIndex < len(nr.Log) && args.PrevLogTerm == nr.Log[args.PrevLogIndex].Term) {
			results.Success = true

			// Truncar el log local si es inconsistente y añadir nuevas entradas.
			// Esta es la implementación estándar y robusta.
			nr.Log = append(nr.Log[:args.PrevLogIndex+1], args.Entries...)
			nr.Logger.Printf("log truncado y actualizado. Nuevo log: %v", nr.Log)

			// Actualizar commitIndex
			if args.LeaderCommit > nr.CommitIndex {
				nr.CommitIndex = min(args.LeaderCommit, len(nr.Log)-1)
				nr.Logger.Printf("actualizando commitIndex a %d", nr.CommitIndex)
				nr.NewCommitReadyChan <- struct{}{}
			}
		}
	}

	results.Term = nr.CurrentTerm
	nr.Logger.Printf("Respuesta AppendEntries: %+v", *results)
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ----- Metodos/Funciones a utilizar como clientes

func (nr *NodoRaft) enviarPeticionVoto(nodo int, args *ArgsPeticionVoto, reply *RespuestaPeticionVoto) bool {
	timeout := 50 * time.Millisecond
	err := nr.Nodos[nodo].CallTimeout("NodoRaft.PedirVoto", args, reply, timeout)
	return err == nil
}

func (nr *NodoRaft) enviarAppendEntries(nodo int, args *ArgAppendEntries, results *Results) bool {
	timeout := 50 * time.Millisecond
	err := nr.Nodos[nodo].CallTimeout("NodoRaft.AppendEntries", args, results, timeout)
	return err == nil
}

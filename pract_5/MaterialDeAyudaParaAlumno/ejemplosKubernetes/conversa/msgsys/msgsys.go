package msgsys

import (
	"encoding/gob"
	//"fmt"
	"log"
	"net"
	"time"
)

type HostPuerto string // "nombredns:puerto" o "numIP:puerto"

type Message struct {
	Contenido string
	Remitente HostPuerto
}

const (
	// Dato no conocido
	//HOSTINDEFINIDO = "HOST INDEFINIDO"
	// Máximo nº de mensajes en mailbox
	MAXMESSAGES = 100
)

type MsgSys struct {
	me       HostPuerto
	listener net.Listener
	buzon    chan Message
	done     chan struct{}
	tmr      *time.Timer
}

func checkError(err error, comment string) {
	if err != nil {
		log.SetFlags(log.Lmicroseconds)
		log.Fatalf("Fatal error --- %s -- %s\n", err.Error(), comment)
	}
}

func StartMsgSys(me HostPuerto) (ms MsgSys) {
	ms = MsgSys{me: me,
		buzon: make(chan Message, MAXMESSAGES),
		done:  make(chan struct{}),
		tmr:   time.NewTimer(0)}
	// detener y consumir, eventualmente el timer inicializado
	if !ms.tmr.Stop() {
		<-ms.tmr.C
	}

	var err error
	//ip, port, erro := net.SplitHostPort(string(ms.me))
	//_, port, _ := net.SplitHostPort(string(ms.me))
	// quizás correponde a ip:port de un recurso service de Kubernetes
	//luego generalizamos la cha a todas las direcciones del Pod
	// ms.listener, err = net.Listen("tcp", "0.0.0.0:"+port)
	ms.listener, err = net.Listen("tcp", string(ms.me))
	checkError(err, "Problema aceptación en networkReceiver  ")

	log.Println("Process listening at ", string(ms.me))

	// concurrent network listener to this MailBoxRead
	go ms.networkReceiver()

	return ms
}

func (ms MsgSys) Me() HostPuerto {
	return ms.me
}

// Close message system and all its goroutines
func (ms *MsgSys) CloseMessageSystem() {
	//notificar terminacion a la goroutine de escucha networkReceiver
	close(ms.done)

	err := ms.listener.Close() // Cerrar el Accept de networkReceiver
	checkError(err, "Problema en cierre de Listener en CloseMessageSystem")

	close(ms.buzon)

	// Wait a milisecond for goroutine to die
	time.Sleep(time.Millisecond)
}

// network listener to a Channel
func (ms *MsgSys) networkReceiver() {
	for {
		conn, err := ms.listener.Accept() // bloqueo en recepción de red
		if err != nil {                   // en caso de error
			select {
			// puede ser normal por listener.close() de CloseMessageSystem
			case <-ms.done:
				log.Println("STOPPED listening for messages at",
					string(ms.me))
				return // Escucha terminada correctamente para MsgSys !!!!

			// o un error de establecimiento de conexion a notificar
			default:
				checkError(err, "Problema aceptación en New")
			}
		}

		decoder := gob.NewDecoder(conn)
		var msg Message
		err = decoder.Decode(&msg)
		checkError(err, "Problema Decode en mailbox.Make")

		conn.Close()

		ms.buzon <- msg
	}
}

func (ms MsgSys) Send(destinatario HostPuerto, msg Message) (err error) {
	conn, err := net.Dial("tcp", string(destinatario))

	if err != nil {
		log.Printf("Problema con DialTCP en Send -- %s\n", err.Error())
		return err
	}

	// fmt.Printf("Message for encoder: %#v \n", msg)
	encoder := gob.NewEncoder(conn)
	err = encoder.Encode(&msg)
	checkError(err, "Problema con Encode en Send")
	conn.Close()
	return nil
}

// Recepción bloqueante
func (ms *MsgSys) Receive() Message {
	return <-ms.buzon
}

/*
// Send sincrono con respuesta y timeout, true si recibe respuesta, false sino
// timeout en microsegundos
func (ms *MsgSys) SendReceive(destinatario HostPuerto,
	msg Message, timeout time.Duration) (res Message, ok bool) {

	log.SetFlags(log.Lmicroseconds)
	log.Printf("Comienzo SendReceive, destinatario: %s, mensaje: %#v\n",
		destinatario, msg)

	err := ms.Send(destinatario, msg)

	if err != nil {
		log.Printf("Problema con DialTCP en Send de ReceiveSend -- %s\n",
			err.Error())
		return struct{}{}, false
	}

	ms.tmr.Reset(timeout)

	// recibe mensaje o timeout
	select {
	case res = <-ms.buzon:
		fmt.Println("SendReceive before timer.stop")
		if !ms.tmr.Stop() {
			<-ms.tmr.C
		}
		fmt.Println("SendReceive AFTER timer.stop")
		return res, true

	case <-ms.tmr.C:
		log.Printf("SendReceive timeout! destinatario: %s, msg: %#v\n",
			destinatario, msg)

		return struct{}{}, false
	}
}

// Recepción con timeout, true si recibe mensaje, false sino
func (ms *MsgSys) ReceiveTimed(timeout time.Duration) (m Message, ok bool) {
	ms.tmr.Reset(timeout)

	select {
	case m = <-ms.buzon:
		if !ms.tmr.Stop() {
			<-ms.tmr.C
		}
		return m, true

	case <-ms.tmr.C:
		return struct{}{}, false
	}
}

func (ms *MsgSys) ProcessAllMsg(procesaMensaje func(m Message)) {
	for m := range ms.buzon {
		procesaMensaje(m)
	}
}
*/

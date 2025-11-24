/*
Servidor continuo que responde a strings que le llegan con remitente
*/
package main

import (
	"log"
	"conversa/msgsys"
	"os"
)

func main() {
    me := os.Args[1]    //host:puerto
    
	log.Println("Arrancando servidor: ", me)
    ms := msgsys.StartMsgSys(msgsys.HostPuerto(me))

    var menRecib msgsys.Message
    menEnviar := msgsys.Message{Remitente: ms.Me()}
    sigue := true
	for sigue {
	    menRecib = ms.Receive()
	    log.Println("He recibido: ", menRecib.Contenido)
	    menEnviar.Contenido = "Me ha llegado: " + menRecib.Contenido
	    ms.Send(menRecib.Remitente, menEnviar)
	    if menRecib.Contenido == "by" {
	        sigue = false
	    }
	}
}
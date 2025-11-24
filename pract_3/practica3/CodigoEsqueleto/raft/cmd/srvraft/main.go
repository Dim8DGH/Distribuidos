package main

import (
	//"errors"
	//"fmt"
	"fmt"
	"log"
	"net"
	"net/rpc"
	"os"
	"raft/internal/comun/check"
	"raft/internal/comun/rpctimeout"
	"raft/internal/raft"
	"strconv"
	"sync"
	//"time"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Uso: go run main.go <indice-nodo> <endpoint-1> <endpoint-2> ...")
		os.Exit(1)
	}

	// obtener entero de indice de este nodo
	me, err := strconv.Atoi(os.Args[1])
	check.CheckError(err, "Main, mal numero entero de indice de nodo:")

	// Resto de argumento son los end points
	nodos := os.Args[2:]

	// Comprobar que el indice de este nodo es valido
	if me < 0 || me >= len(nodos) {
		log.Printf("Indice de nodo no valido: %d", me)
		os.Exit(1)
	}

	//Creamos la maquina de estados y los canales
	almacen := make(map[string]string)
	almacenMutex := sync.Mutex{}
	canalAplicar := make(chan raft.AplicaOperacion, 100)

	// Goroutina que se encarga de aplicar las operaciones comprometidas
	go func() {
		fmt.Println("Maquina de estados iniciada. Esperando a las operaciones...")
		// Para cada operacion comprometida
		for operacionComprometida := range canalAplicar {
			// Bloqueamos la maquina de estados
			almacenMutex.Lock()
			// Dependiendo de la operacion, escribimos o leemos
			switch operacionComprometida.Operacion.Operacion {
			// Escribir
			case "escribir":
				clave := operacionComprometida.Operacion.Clave
				valor := operacionComprometida.Operacion.Valor
				almacen[clave] = valor
				fmt.Printf("Escribiendo clave %s con valor %s\n", clave, valor)
			// Leer
			case "leer":
				clave := operacionComprometida.Operacion.Clave
				valor := almacen[clave]
				fmt.Printf("Leyendo clave %s con valor %s\n", clave, valor)
			}
			// Desbloqueamos la maquina de estados
			almacenMutex.Unlock()
		}
	}()

	// Parte Servidor
	nr := raft.NuevoNodo(rpctimeout.StringArrayToHostPortArray(nodos), me, canalAplicar)

	err = rpc.Register(nr)
	check.CheckError(err, "Error al iniciar el nodo")
	rpc.HandleHTTP()
	fmt.Println("Replica escucha en :", me, " de ", os.Args[2:])
	l, err := net.Listen("tcp", os.Args[2:][me])
	check.CheckError(err, "Main listen error:")
	rpc.Accept(l)

}

// func main() {
// 	// obtener entero de indice de este nodo
// 	me, err := strconv.Atoi(os.Args[1])
// 	check.CheckError(err, "Main, mal numero entero de indice de nodo:")

// 	var nodos []rpctimeout.HostPort
// 	// Resto de argumento son los end points como strings
// 	// De todas la replicas-> pasarlos a HostPort
// 	for _, endPoint := range os.Args[2:] {
// 		nodos = append(nodos, rpctimeout.HostPort(endPoint))
// 	}

// 	// Parte Servidor
// 	nr := raft.NuevoNodo(nodos, me, make(chan raft.AplicaOperacion, 1000))
// 	rpc.Register(nr)

// 	fmt.Println("Replica escucha en :", me, " de ", os.Args[2:])

// 	l, err := net.Listen("tcp", os.Args[2:][me])
// 	check.CheckError(err, "Main listen error:")

// 	rpc.Accept(l)
// }

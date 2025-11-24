package main

import (
	"fmt"
	"os"
	"raft/internal/comun/rpctimeout"
	"raft/internal/raft"
	"time"
)

func usage() {
	fmt.Println("Uso: go run ./cmd/cltraft <comando> [argumentos...]")
	fmt.Println("Ejemplos:")
	fmt.Println("  go run ./cmd/cltraft status 127.0.0.1:29001 127.0.0.1:29002")
	fmt.Println("  go run ./cmd/cltraft put clave valor 127.0.0.1:29001")
	fmt.Println("\nComandos:")
	fmt.Println("  status <endpoint-1> ...            : Muestra el estado de los nodos del clúster.")
	fmt.Println("  put <clave> <valor> <endpoint-1> ... : Escribe una clave y valor en el clúster.")
	fmt.Println("  get <clave> <endpoint-1> ...         : Lee una clave del clúster.")
	os.Exit(1)
}

// Funcion main del programa
func main() {
	time.Sleep(3*time.Second)
	// En caso de que el uso sea incorrecto imprimimos el menu de explicacion del cliente
	if len(os.Args) < 3 {
		usage()
	}

	command := os.Args[1]

	var nodes []rpctimeout.HostPort
	var startNodeArgs int

	switch command {
	//Obtener estado de los nodos
	case "status":
		startNodeArgs = 2
	// Insertar un valor
	case "put":
		// En caso de tener numero de parametros incorrecto para la insercion
		if len(os.Args) < 5 {
			usage()
		}
		startNodeArgs = 4
	// Obtener un valor
	case "get":
		if len(os.Args) < 4 {
			usage()
		}
		startNodeArgs = 3

	default:
		fmt.Println("Comando no reconocido: ", command)
		usage()
	}

	// Convertimos el array
	nodes = rpctimeout.StringArrayToHostPortArray(os.Args[startNodeArgs:])
	// Si la conversion no tiene éxito
	if len(nodes) == 0 {
		fmt.Println("Numero de nodos incorrecto.")
		usage()
	}

	// Ejecutamos el comando que se ha introducido via de comandos
	switch command {
	case "status":
		getStatus(nodes)
	case "put":
		key := os.Args[2]
		value := os.Args[3]
		put(nodes, key, value)
	case "get":
		key := os.Args[2]
		get(nodes, key)
	}
}

func getStatus(nodes []rpctimeout.HostPort) {
	fmt.Println("Estado del cluster: ")
	leaderFound := false
	for i, node := range nodes {
		var reply raft.EstadoRemoto
		err := node.CallTimeout("NodoRaft.ObtenerEstadoNodo", raft.Vacio{}, &reply, 5*time.Second)
		// Si la llamada nos devuelve un error
		if err != nil {
			fmt.Printf("  Nodo %d (%s): OFFLINE - %v\n", i, node, err)
			continue
		}
		status := "Seguidor"

		// Si la respuesta nos dice que es líder actualizamos el estado a lider
		if reply.EsLider {
			status = "Lider"
			leaderFound = true
		}
		fmt.Printf("  Nodo %d (%s): ONLINE - Rol=%s, Mandato=%d\n", reply.IdNodo, node, status, reply.Mandato)
	}
	// En caso de no encontrar un lider en el cluster lo indicamos
	if !leaderFound {
		fmt.Println("\nNo se pudo contactar con un líder.")
	}
}

// Funcion que dentro de una lista de nodos busca el nodo líder
func findLeader(nodes []rpctimeout.HostPort) (rpctimeout.HostPort, error) {
	for _, node := range nodes {
		var reply raft.EstadoRemoto
		err := node.CallTimeout("NodoRaft.ObtenerEstadoNodo", raft.Vacio{}, &reply, 5*time.Second)

		if err == nil && reply.EsLider {
			//Devolver el nodo que es el líder
			return node, nil
		}
	}

	return "", fmt.Errorf("no se pudo encontrar un líder en la lista de nodos")
}

// Funcion que inserta un valor en los registros del cluster de raft
func put(nodes []rpctimeout.HostPort, key, value string) {
	leaderNode, err := findLeader(nodes)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Contactando con el líder en %s para escribir '%s'='%s'\n", leaderNode, key, value)

	operation := raft.TipoOperacion{
		Operacion: "escribir",
		Clave:     key,
		Valor:     value,
	}

	var reply raft.ResultadoRemoto
	err = leaderNode.CallTimeout("NodoRaft.SometerOperacionRaft", operation, &reply, 5*time.Second)
	if err != nil {
		fmt.Printf("Error al enviar la operación: %v\n", err)
		return
	}
	
	if !reply.EsLider {
		fmt.Println("Error, ha habido un cambio de líder en el proceso de la operacion.")
		return
	}

	fmt.Printf("Operación completada. La entrada fue sometida en el índice de log: %d\n", reply.IndiceRegistro)
}

func get(nodes []rpctimeout.HostPort, key string) {
	leaderNode, err := findLeader(nodes)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Contactando con el líder en %s para leer '%s'\n", leaderNode, key)

	operation := raft.TipoOperacion{
		Operacion: "leer",
		Clave:     key,
	}

	var reply raft.ResultadoRemoto

	err = leaderNode.CallTimeout("NodoRaft.SometerOperacionRaft", operation, &reply, 5*time.Second)
	if err != nil {
		fmt.Printf("Error al enviar la operación: %v\n", err)
		return
	}

	if !reply.EsLider {
		fmt.Println("Error, ha habido un cambio de líder en el proceso de la operacion.")
		return
	}

	fmt.Printf("Operación de lectura enviada. El resultado aparecerá en la consola de los servidores Raft.\n")
	fmt.Printf("La operación fue sometida en el índice de log: %d\n", reply.IndiceRegistro)
}

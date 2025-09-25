/*
* AUTOR: Rafael Tolosana Calasanz y Unai Arronategui
* ASIGNATURA: 30221 Sistemas Distribuidos del Grado en Ingeniería Informática
* Escuela de Ingeniería y Arquitectura - Universidad de Zaragoza
* FECHA: septiembre de 2022
* FICHERO: server-draft.go
* DESCRIPCIÓN: contiene la funcionalidad esencial para realizar los servidores
* correspondientes a la práctica 1
 */
package main

import (
	"encoding/gob"
	"log"
	"net"
	"os"
	"practica1/com"
)

// PRE: verdad = !foundDivisor
// POST: IsPrime devuelve verdad si n es primo y falso en caso contrario
func isPrime(n int) (foundDivisor bool) {
	foundDivisor = false
	for i := 2; (i < n) && !foundDivisor; i++ {
		foundDivisor = (n%i == 0)
	}
	return !foundDivisor
}

// PRE: interval.A < interval.B
// POST: FindPrimes devuelve todos los números primos comprendidos en el
//
//	intervalo [interval.A, interval.B]
func findPrimes(interval com.TPInterval) (primes []int) {
	for i := interval.Min; i <= interval.Max; i++ {
		if isPrime(i) {
			primes = append(primes, i)
		}
	}
	return primes
}

// Goroutine para atender las peticiones
func goRut(entradaChannel <-chan net.Conn) {
	for {
		var request com.Request
		var reply com.Reply
		// Esperamos que el cliente meta una conexión en la tubería
		conn := <-entradaChannel

		// Recibimos la request del cliente
		decoder := gob.NewDecoder(conn)
		err := decoder.Decode(&request)
		if err != nil {
			log.Println("Error al decodificar la petición:", err)
			conn.Close()
			continue
		}

		log.Printf("Petición recibida: %v\n", request)

		primes := findPrimes(request.Interval)
		reply = com.Reply{Id: request.Id, Primes: primes}

		// Enviamos la respuesta al cliente
		encoder := gob.NewEncoder(conn)
		err = encoder.Encode(&reply)
		if err != nil {
			log.Println("Error al enviar la respuesta:", err)
		}

		log.Printf("Respuesta enviada para petición %d: %v\n", request.Id, reply.Primes)

		//  cerramos la conexión
		conn.Close()
	}
}

func main() {
	entradaChannel := make(chan net.Conn)

	args := os.Args
	if len(args) != 2 {
		log.Println("Error: endpoint missing: go run server.go ip:port")
		os.Exit(1)
	}

	endpoint := args[1]
	listener, err := net.Listen("tcp", endpoint)
	com.CheckError(err)

	log.SetFlags(log.Lshortfile | log.Lmicroseconds)
	log.Println("***** Listening for new connection in endpoint ", endpoint)

	// Creamos varias Goroutines para atender las peticiones concurrentemente
	for i := 0; i < 4; i++ {
		go goRut(entradaChannel)
	}

	for {
		conn, err := listener.Accept()
		com.CheckError(err)

		// Metemos la conexión en la tubería para que una de las Goroutines la atienda
		entradaChannel <- conn
	}
}

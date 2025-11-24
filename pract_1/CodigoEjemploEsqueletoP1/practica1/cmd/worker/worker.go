package main

import (
	"encoding/gob"
	"fmt"
	"log"
	"net"
	"os"
)

// Estructura para la tarea recibida del master
type PrimeTask struct {
	Start int
	End   int
}

// Estructura para el resultado que se envía al master
type PrimeResult struct {
	Primes []int
}

// Simula la función FindPrimes (debes importar la real en la práctica)
func FindPrimes(start, end int) []int {
	// Aquí deberías llamar a la función real que tienes implementada
	primes := []int{}
	for i := start; i <= end; i++ {
		if IsPrime(i) {
			primes = append(primes, i)
		}
	}
	return primes
}

// Simula la función IsPrime (debes importar la real en la práctica)
func IsPrime(n int) bool {
	if n < 2 {
		return false
	}
	for i := 2; i*i <= n; i++ {
		if n%i == 0 {
			return false
		}
	}
	return true
}

func main() {
	args := os.Args
	if len(args) != 2 {
		log.Println("Error: numero de parametros invalido: ./worker <port>")
		os.Exit(1)
	}

	port := args[1] // Cambia el puerto según lo que pongas en machines.txt

	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("Error al iniciar servidor: %v", err)
	}
	fmt.Printf("Worker escuchando en el puerto %s\n", port)

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Println("Error al aceptar conexión:", err)
			continue
		}
		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()

	// Recibir la tarea
	decoder := gob.NewDecoder(conn)
	var task PrimeTask
	if err := decoder.Decode(&task); err != nil {
		log.Println("Error al recibir tarea:", err)
		return
	}

	// Calcular los primos en el intervalo
	primes := FindPrimes(task.Start, task.End)

	// Enviar el resultado al master
	result := PrimeResult{Primes: primes}
	encoder := gob.NewEncoder(conn)
	if err := encoder.Encode(result); err != nil {
		log.Println("Error al enviar resultado:", err)
		return
	}

	fmt.Printf("Worker procesó intervalo [%d, %d]: %d primos\n", task.Start, task.End, len(primes))
}

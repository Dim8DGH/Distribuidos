package main

import (
	"bufio"
	"encoding/gob"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
)

// Estructura para guardar datos de las máquinas
type Machine struct {
	IP   string
	Port string
}

// Estructura para la tarea que se envía al worker
type PrimeTask struct {
	Start int
	End   int
}

// Estructura para el resultado que devuelve el worker
type PrimeResult struct {
	Primes []int
}

func main() {
	// Verificar argumentos de línea de comandos
	if len(os.Args) < 2 {
		log.Fatalf("Uso: %s <archivo_maquinas>", os.Args[0])
	}
	
	// Leer el fichero de máquinas (IPs + puertos)
	machineFile := os.Args[1]
	machines, err := readMachines(machineFile)
	if err != nil {
		log.Fatalf("Error leyendo fichero de máquinas: %v", err)
	}
	
	// Verificar que hay máquinas disponibles
	if len(machines) == 0 {
		log.Fatalf("No se encontraron máquinas en el fichero %s", machineFile)
	}
	
	// Definir el intervalo general de búsqueda
	globalStart := 1000
	globalEnd := 70000
	// Calcular sub-intervalos para cada worker
	numWorkers := len(machines)
	intervalSize := (globalEnd - globalStart + 1) / numWorkers
	tasks := make([]PrimeTask, numWorkers)
	for i := 0; i < numWorkers; i++ {
		start := globalStart + i*intervalSize
		end := start + intervalSize - 1
		if i == numWorkers-1 {
			end = globalEnd // Asegura que el último worker llegue al final del intervalo
		}
		tasks[i] = PrimeTask{Start: start, End: end}
	}
	// Lanzar los workers y enviarles la tarea
	results := make([]PrimeResult, numWorkers)
	var failedTasks []PrimeTask  // NUEVO: Lista de tareas fallidas
	var workingMachines []int    // NUEVO: Índices de máquinas que funcionan

	for i, m := range machines {
		fmt.Printf("Conectando a worker en %s:%s para intervalo [%d, %d]\n", m.IP, m.Port, tasks[i].Start, tasks[i].End)
		conn, err := net.Dial("tcp", fmt.Sprintf("%s:%s", m.IP, m.Port))
		if err != nil {
			log.Printf("No se pudo conectar a %s:%s: %v", m.IP, m.Port, err)
			failedTasks = append(failedTasks, tasks[i]) // NUEVO: Guardar tarea fallida
			continue
		}
		// Enviar la tarea usando gob
		encoder := gob.NewEncoder(conn)
		if err := encoder.Encode(tasks[i]); err != nil {
			log.Printf("Error enviando tarea al worker: %v", err)
			conn.Close()
			failedTasks = append(failedTasks, tasks[i]) // NUEVO: Guardar tarea fallida
			continue
		}
		// Recibir el resultado usando gob
		decoder := gob.NewDecoder(conn)
		var result PrimeResult
		if err := decoder.Decode(&result); err != nil {
			log.Printf("Error recibiendo resultado del worker: %v", err)
			conn.Close()
			failedTasks = append(failedTasks, tasks[i]) // NUEVO: Guardar tarea fallida
			continue
		}
		results[i] = result
		workingMachines = append(workingMachines, i) // NUEVO: Guardar índice de máquina funcionando
		conn.Close()
	}

	// NUEVO: Redistribuir tareas fallidas entre máquinas que funcionan
	if len(failedTasks) > 0 && len(workingMachines) > 0 {
		fmt.Printf("Redistribuyendo %d tareas fallidas entre %d máquinas disponibles\n", len(failedTasks), len(workingMachines))
		redistributeFailedTasks(machines, workingMachines, failedTasks, &results)
	}

	// Unir todos los resultados
	var allPrimes []int
	for _, r := range results {
		allPrimes = append(allPrimes, r.Primes...)
	}
	fmt.Printf("Total de primos encontrados: %d\n", len(allPrimes))
	// Puedes imprimirlos o guardarlos en un fichero si quieres
}

// NUEVA FUNCIÓN: Redistribuir tareas fallidas
func redistributeFailedTasks(machines []Machine, workingIndices []int, failedTasks []PrimeTask, results *[]PrimeResult) {
	for i, task := range failedTasks {
		// Seleccionar máquina de trabajo de forma circular
		workerIdx := workingIndices[i%len(workingIndices)]
		machine := machines[workerIdx]

		fmt.Printf("Redistribuyendo tarea [%d, %d] a worker %s:%s\n", task.Start, task.End, machine.IP, machine.Port)

		conn, err := net.Dial("tcp", fmt.Sprintf("%s:%s", machine.IP, machine.Port))
		if err != nil {
			log.Printf("Error en redistribución a %s:%s: %v", machine.IP, machine.Port, err)
			continue
		}

		// Enviar la tarea redistribuida
		encoder := gob.NewEncoder(conn)
		if err := encoder.Encode(task); err != nil {
			log.Printf("Error enviando tarea redistribuida: %v", err)
			conn.Close()
			continue
		}

		// Recibir el resultado
		decoder := gob.NewDecoder(conn)
		var result PrimeResult
		if err := decoder.Decode(&result); err != nil {
			log.Printf("Error recibiendo resultado redistribuido: %v", err)
			conn.Close()
			continue
		}

		// Agregar resultado a los existentes
		(*results)[workerIdx].Primes = append((*results)[workerIdx].Primes, result.Primes...)
		conn.Close()

		fmt.Printf("Tarea redistribuida completada: %d primos encontrados\n", len(result.Primes))
	}
}

// Función para leer el fichero de máquinas
func readMachines(filename string) ([]Machine, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var machines []Machine
	
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) == 2 {
			machines = append(machines, Machine{IP: parts[0], Port: parts[1]})
		}
	}
	return machines, scanner.Err()
}

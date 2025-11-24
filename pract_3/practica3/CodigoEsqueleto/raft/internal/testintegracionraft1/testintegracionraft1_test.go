package testintegracionraft1

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"raft/internal/comun/check"
	"raft/internal/comun/rpctimeout"
	"raft/internal/despliegue"
	"raft/internal/raft"
)

const (
	//hosts
	MAQUINA1 = "127.0.0.1"
	MAQUINA2 = "127.0.0.1"
	MAQUINA3 = "127.0.0.1"

	//puertos
	PUERTOREPLICA1 = "29001"
	PUERTOREPLICA2 = "29002"
	PUERTOREPLICA3 = "29003"

	//nodos replicas
	REPLICA1 = MAQUINA1 + ":" + PUERTOREPLICA1
	REPLICA2 = MAQUINA2 + ":" + PUERTOREPLICA2
	REPLICA3 = MAQUINA3 + ":" + PUERTOREPLICA3

	// paquete main de ejecutables relativos a PATH previo
	EXECREPLICA = "cmd/srvraft/main.go"

	// comandos completo a ejecutar en máquinas remota con ssh. Ejemplo :
	// 				cd $HOME/raft; go run cmd/srvraft/main.go 127.0.0.1:29001

	// Ubicar, en esta constante, nombre de fichero de vuestra clave privada local
	// emparejada con la clave pública en authorized_keys de máquinas remotas

	PRIVKEYFILE = "id_ed25519"
)

// PATH de los ejecutables de modulo golang de servicio Raft
var PATH string = filepath.Join(os.Getenv("HOME"), "Documents", "ClassShit", "4", "dist", "pract_3", "practica3", "CodigoEsqueleto", "raft")

// go run cmd/srvraft/main.go 0 127.0.0.1:29001 127.0.0.1:29002 127.0.0.1:29003
var EXECREPLICACMD string = "cd " + PATH + "; go run " + EXECREPLICA

// TEST primer rango
func TestPrimerasPruebas(t *testing.T) { // (m *testing.M) {
	// <setup code>
	// Crear canal de resultados de ejecuciones ssh en maquinas remotas
	cfg := makeCfgDespliegue(t,
		3,
		[]string{REPLICA1, REPLICA2, REPLICA3},
		[]bool{true, true, true})

	// tear down code
	// eliminar procesos en máquinas remotas
	defer cfg.stop()

	// Run test sequence

	// Test1 : No debería haber ningun primario, si SV no ha recibido aún latidos
	t.Run("T1:soloArranqueYparada",
		func(t *testing.T) { cfg.soloArranqueYparadaTest1(t) })

	// Test2 : No debería haber ningun primario, si SV no ha recibido aún latidos
	t.Run("T2:ElegirPrimerLider",
		func(t *testing.T) { cfg.elegirPrimerLiderTest2(t) })

	// Test3: tenemos el primer primario correcto
	t.Run("T3:FalloAnteriorElegirNuevoLider",
		func(t *testing.T) { cfg.falloAnteriorElegirNuevoLiderTest3(t) })

	// Test4: Tres operaciones comprometidas en configuración estable
	t.Run("T4:tresOperacionesComprometidasEstable",
		func(t *testing.T) { cfg.tresOperacionesComprometidasEstable(t) })
}

// TEST primer rango
func TestAcuerdosConFallos(t *testing.T) { // (m *testing.M) {
	// <setup code>
	// Crear canal de resultados de ejecuciones ssh en maquinas remotas
	cfg := makeCfgDespliegue(t,
		3,
		[]string{REPLICA1, REPLICA2, REPLICA3},
		[]bool{true, true, true})

	// tear down code
	// eliminar procesos en máquinas remotas
	defer cfg.stop()

	// Test5: Se consigue acuerdo a pesar de desconexiones de seguidor
	t.Run("T5:AcuerdoAPesarDeDesconexionesDeSeguidor ",
		func(t *testing.T) { cfg.AcuerdoApesarDeSeguidor(t) })

	t.Run("T5:SinAcuerdoPorFallos ",
		func(t *testing.T) { cfg.SinAcuerdoPorFallos(t) })

	t.Run("T5:SometerConcurrentementeOperaciones ",
		func(t *testing.T) { cfg.SometerConcurrentementeOperaciones(t) })

}

// ---------------------------------------------------------------------
//
// Canal de resultados de ejecución de comandos ssh remotos
type canalResultados chan string

func (cr canalResultados) stop() {
	close(cr)

	// Leer las salidas obtenidos de los comandos ssh ejecutados
	for s := range cr {
		fmt.Println(s)
	}
}

// ---------------------------------------------------------------------
// Operativa en configuracion de despliegue y pruebas asociadas
type configDespliegue struct {
	t           *testing.T
	conectados  []bool
	numReplicas int
	nodosRaft   []rpctimeout.HostPort
	cr          canalResultados
}

// Crear una configuracion de despliegue
func makeCfgDespliegue(t *testing.T, n int, nodosraft []string,
	conectados []bool) *configDespliegue {
	cfg := &configDespliegue{}
	cfg.t = t
	cfg.conectados = conectados
	cfg.numReplicas = n
	cfg.nodosRaft = rpctimeout.StringArrayToHostPortArray(nodosraft)
	cfg.cr = make(canalResultados, 2000)

	return cfg
}

func (cfg *configDespliegue) stop() {
	//cfg.stopDistributedProcesses()

	time.Sleep(50 * time.Millisecond)

	cfg.cr.stop()
}

// --------------------------------------------------------------------------
// FUNCIONES DE SUBTESTS

// Se pone en marcha una replica ?? - 3 NODOS RAFT
func (cfg *configDespliegue) soloArranqueYparadaTest1(t *testing.T) {
	t.Skip("SKIPPED soloArranqueYparadaTest1")

	fmt.Println(t.Name(), ".....................")

	cfg.t = t // Actualizar la estructura de datos de tests para errores

	// Poner en marcha replicas en remoto con un tiempo de espera incluido
	cfg.startDistributedProcesses()

	// Comprobar estado replica 0
	cfg.comprobarEstadoRemoto(0, 0, false, -1)

	// Comprobar estado replica 1
	cfg.comprobarEstadoRemoto(1, 0, false, -1)

	// Comprobar estado replica 2
	cfg.comprobarEstadoRemoto(2, 0, false, -1)

	// Parar réplicas almacenamiento en remoto
	cfg.stopDistributedProcesses()

	fmt.Println(".............", t.Name(), "Superado")
}

// Primer lider en marcha - 3 NODOS RAFT
func (cfg *configDespliegue) elegirPrimerLiderTest2(t *testing.T) {
	// t.Skip("SKIPPED ElegirPrimerLiderTest2")

	fmt.Println(t.Name(), ".....................")

	cfg.startDistributedProcesses()

	// Se ha elegido lider ?
	fmt.Printf("Probando lider en curso\n")
	cfg.pruebaUnLider(3)

	// Parar réplicas alamcenamiento en remoto
	cfg.stopDistributedProcesses() // Parametros

	fmt.Println(".............", t.Name(), "Superado")
}

// Fallo de un primer lider y reeleccion de uno nuevo - 3 NODOS RAFT
func (cfg *configDespliegue) falloAnteriorElegirNuevoLiderTest3(t *testing.T) {
	// t.Skip("SKIPPED FalloAnteriorElegirNuevoLiderTest3")

	fmt.Println(t.Name(), ".....................")

	cfg.startDistributedProcesses()

	fmt.Printf("Lider inicial\n")
	cfg.pruebaUnLider(3)

	// Desconectar lider
	// ???

	fmt.Printf("Comprobar nuevo lider\n")
	cfg.pruebaUnLider(3)

	// Parar réplicas almacenamiento en remoto
	cfg.stopDistributedProcesses() //parametros

	fmt.Println(".............", t.Name(), "Superado")
}

// 3 operaciones comprometidas con situacion estable y sin fallos - 3 NODOS RAFT
func (cfg *configDespliegue) tresOperacionesComprometidasEstable(t *testing.T) {
	fmt.Println(t.Name(), ".....................")

	cfg.t = t
	cfg.startDistributedProcesses()
	defer cfg.stopDistributedProcesses()

	liderInicial := cfg.pruebaUnLider(3)
	_, mandatoInicial, _, _ := cfg.obtenerEstadoRemoto(liderInicial)

	operaciones := []raft.TipoOperacion{
		{Operacion: "escribir", Clave: "clave1", Valor: "valor1"},
		{Operacion: "escribir", Clave: "clave2", Valor: "valor2"},
		{Operacion: "escribir", Clave: "clave3", Valor: "valor3"},
	}

	for indiceEsperado, operacion := range operaciones {
		resultado := cfg.someterOperacionRaft(liderInicial, operacion)
		if !resultado.EsLider || resultado.IdLider != liderInicial {
			t.Fatalf("La operación %d no fue aceptada por el líder esperado %d (resultado %+v)",
				indiceEsperado, liderInicial, resultado)
		}
		if resultado.IndiceRegistro != indiceEsperado {
			t.Fatalf("Índice de registro incorrecto. Esperado %d, obtenido %d",
				indiceEsperado, resultado.IndiceRegistro)
		}
	}

	time.Sleep(1 * time.Second)

	liderEstable := cfg.pruebaUnLider(3)
	_, mandatoFinal, _, _ := cfg.obtenerEstadoRemoto(liderEstable)

	if mandatoFinal != mandatoInicial {
		t.Fatalf("Se esperaba mantener el mismo mandato (%d), pero se obtuvo %d tras someter operaciones",
			mandatoInicial, mandatoFinal)
	}

	for nodo := 0; nodo < cfg.numReplicas; nodo++ {
		cfg.comprobarEstadoRemoto(nodo, mandatoFinal, nodo == liderEstable, liderEstable)
	}

	fmt.Println(".............", t.Name(), "Superado")
}

// Se consigue acuerdo a pesar de desconexiones de seguidor -- 3 NODOS RAFT
func (cfg *configDespliegue) AcuerdoApesarDeSeguidor(t *testing.T) {
	fmt.Println(t.Name(), ".....................")

	cfg.t = t
	cfg.startDistributedProcesses()
	defer cfg.stopDistributedProcesses()

	liderInicial := cfg.pruebaUnLider(3)
	seguidorDesconectado := (liderInicial + 1) % cfg.numReplicas

	fmt.Printf("Desconectando seguidor %d\n", seguidorDesconectado)
	cfg.detenerReplica(seguidorDesconectado)

	operaciones := []raft.TipoOperacion{
		{Operacion: "escribir", Clave: "claveA", Valor: "valorA"},
		{Operacion: "escribir", Clave: "claveB", Valor: "valorB"},
		{Operacion: "escribir", Clave: "claveC", Valor: "valorC"},
	}

	for indice, operacion := range operaciones {
		cfg.t.Logf("-> Sometiendo op: %+v", operacion)
		resultado := cfg.someterOperacionRaft(liderInicial, operacion)
		cfg.t.Logf("<- Resultado op: %+v", resultado)
		if !resultado.EsLider || resultado.IdLider != liderInicial {
			t.Fatalf("Operación %d rechazada por el líder esperado %d (%+v)",
				indice, liderInicial, resultado)
		}
		if resultado.IndiceRegistro != indice {
			t.Fatalf("Índice comprometido inesperado: esperado %d, obtenido %d",
				indice, resultado.IndiceRegistro)
		}
		time.Sleep(600 * time.Millisecond)
	}

	fmt.Printf("Reconectando seguidor %d\n", seguidorDesconectado)
	cfg.arrancarReplica(seguidorDesconectado)
	time.Sleep(2 * time.Second)

	liderFinal := cfg.pruebaUnLider(3)
	_, mandatoFinal, _, _ := cfg.obtenerEstadoRemoto(liderFinal)
	for nodo := 0; nodo < cfg.numReplicas; nodo++ {
		cfg.comprobarEstadoRemoto(nodo, mandatoFinal, nodo == liderFinal, liderFinal)
	}

	fmt.Println(".............", t.Name(), "Superado")
}

// NO se consigue acuerdo al desconectarse mayoría de seguidores -- 3 NODOS RAFT
func (cfg *configDespliegue) SinAcuerdoPorFallos(t *testing.T) {
	fmt.Println(t.Name(), ".....................")

	cfg.t = t
	cfg.startDistributedProcesses()
	defer cfg.stopDistributedProcesses()

	liderInicial := cfg.pruebaUnLider(3)
	var seguidores []int
	for i := 0; i < cfg.numReplicas; i++ {
		if i != liderInicial {
			seguidores = append(seguidores, i)
		}
	}
	if len(seguidores) != 2 {
		t.Fatalf("Se esperaban exactamente dos seguidores, obtenidos %d", len(seguidores))
	}

	cfg.detenerReplica(liderInicial)
	cfg.detenerReplica(seguidores[0])

	time.Sleep(1 * time.Second)

	if cfg.contarConectados() >= cfg.numReplicas/2+1 {
		fmt.Println("Advertencia: el número de réplicas conectadas alcanzó la mayoría inesperadamente")
		t.Fatalf("El número de réplicas conectadas no debería alcanzar la mayoría tras desconexiones")
	}

	if _, ok := cfg.esperarLiderConMayoria(3 * time.Second); ok {
		t.Fatalf("No se debería obtener líder con mayoría desconectada")
	}

	cfg.t.Logf("-> Sometiendo op (se espera que falle): %+v", raft.TipoOperacion{Operacion: "escribir", Clave: "clave_fallida", Valor: "valor_fallido"})
	_, err := cfg.someterOperacionRaftNoFatal(liderInicial, raft.TipoOperacion{Operacion: "escribir", Clave: "clave_fallida", Valor: "valor_fallido"})

	if err != nil {
		cfg.t.Logf("<- Resultado op: Se ha producido un error como se esperaba: %v", err)
	} else {
		t.Fatalf("La operación debería haber fallado, pero se completó inesperadamente")
	}

	cfg.arrancarReplica(liderInicial)
	cfg.arrancarReplica(seguidores[0])
	time.Sleep(2 * time.Second)

	liderRecuperado := cfg.pruebaUnLider(3)
	_, mandatoRecuperado, _, _ := cfg.obtenerEstadoRemoto(liderRecuperado)
	for nodo := 0; nodo < cfg.numReplicas; nodo++ {
		cfg.comprobarEstadoRemoto(nodo, mandatoRecuperado, nodo == liderRecuperado, liderRecuperado)
	}

	fmt.Println(".............", t.Name(), "Superado")
}

// Se somete 5 operaciones de forma concurrente -- 3 NODOS RAFT
func (cfg *configDespliegue) SometerConcurrentementeOperaciones(t *testing.T) {
	fmt.Println(t.Name(), ".....................")

	cfg.t = t
	cfg.startDistributedProcesses()
	defer cfg.stopDistributedProcesses()

	lider := cfg.pruebaUnLider(3)

	const numOperaciones = 5
	var wg sync.WaitGroup
	resultados := make([]raft.ResultadoRemoto, numOperaciones)

	for i := 0; i < numOperaciones; i++ {
		wg.Add(1)
		go func(indice int) {
			defer wg.Done()
			op := raft.TipoOperacion{
				Operacion: "escribir",
				Clave:     fmt.Sprintf("clave%d", indice),
				Valor:     fmt.Sprintf("valor%d", indice),
			}
			cfg.t.Logf("-> [Concurrente %d] Sometiendo op: %+v", indice, op)
			resultados[indice] = cfg.someterOperacionRaft(lider, op)
			cfg.t.Logf("<- [Concurrente %d] Resultado op: %+v", indice, resultados[indice])
		}(i)
	}

	wg.Wait()

	indices := make(map[int]struct{})
	for i, res := range resultados {
		if !res.EsLider || res.IdLider != lider {
			t.Fatalf("Resultado inesperado en operación %d: %+v", i, res)
		}
		indices[res.IndiceRegistro] = struct{}{}
	}
	if len(indices) != numOperaciones {
		t.Fatalf("Se esperaban %d índices únicos, obtenidos %d", numOperaciones, len(indices))
	}

	time.Sleep(2 * time.Second)

	liderFinal := cfg.pruebaUnLider(3)
	_, mandatoFinal, _, _ := cfg.obtenerEstadoRemoto(liderFinal)
	for nodo := 0; nodo < cfg.numReplicas; nodo++ {
		cfg.comprobarEstadoRemoto(nodo, mandatoFinal, nodo == liderFinal, liderFinal)
	}

	fmt.Println(".............", t.Name(), "Superado")
}

// --------------------------------------------------------------------------
// FUNCIONES DE APOYO
// Comprobar que hay un solo lider
// probar varias veces si se necesitan reelecciones
func (cfg *configDespliegue) pruebaUnLider(numreplicas int) int {
	for iters := 0; iters < 10; iters++ {
		time.Sleep(500 * time.Millisecond)
		mapaLideres := make(map[int][]int)
		for i := 0; i < numreplicas; i++ {
			if cfg.conectados[i] {
				if _, mandato, eslider, _ := cfg.obtenerEstadoRemoto(i); eslider {
					mapaLideres[mandato] = append(mapaLideres[mandato], i)
				}
			}
		}

		ultimoMandatoConLider := -1
		for mandato, lideres := range mapaLideres {
			if len(lideres) > 1 {
				cfg.t.Fatalf("mandato %d tiene %d (>1) lideres",
					mandato, len(lideres)) 
			}
			if mandato > ultimoMandatoConLider {
				ultimoMandatoConLider = mandato
			}
		}

		if len(mapaLideres) != 0 {

			return mapaLideres[ultimoMandatoConLider][0] // Termina

		}
	}
	cfg.t.Fatalf("un lider esperado, ninguno obtenido")

	return -1 // Termina
}

func (cfg *configDespliegue) obtenerEstadoRemoto(
	indiceNodo int) (int, int, bool, int) {
	var reply raft.EstadoRemoto
	err := cfg.nodosRaft[indiceNodo].CallTimeout("NodoRaft.ObtenerEstadoNodo",
		raft.Vacio{}, &reply, 10*time.Millisecond)
	check.CheckError(err, "Error en llamada RPC ObtenerEstadoRemoto")

	return reply.IdNodo, reply.Mandato, reply.EsLider, reply.IdLider
}

// start  gestor de vistas; mapa de replicas y maquinas donde ubicarlos;
// y lista clientes (host:puerto)
func (cfg *configDespliegue) startDistributedProcesses() {
	//cfg.t.Log("Before starting following distributed processes: ", cfg.nodosRaft)

	for i, endPoint := range cfg.nodosRaft {
		despliegue.ExecMutipleHosts(EXECREPLICACMD+
			" "+strconv.Itoa(i)+" "+
			rpctimeout.HostPortArrayToString(cfg.nodosRaft), 
			[]string{endPoint.Host()}, cfg.cr, PRIVKEYFILE)

		// dar tiempo para se establezcan las replicas
		time.Sleep(500 * time.Millisecond)
		cfg.conectados[i] = true
	}

	// aproximadamente 500 ms para cada arranque por ssh en portatil
	time.Sleep(2500 * time.Millisecond)
}

func (cfg *configDespliegue) stopDistributedProcesses() {
	var reply raft.Vacio

	for indice, endPoint := range cfg.nodosRaft {
		if !cfg.conectados[indice] {
			continue
		}
		err := endPoint.CallTimeout("NodoRaft.ParaNodo",
			raft.Vacio{}, &reply, 100*time.Millisecond)
		if err != nil && cfg.t != nil {
			cfg.t.Logf("Aviso al detener nodo %d: %v", indice, err)
			continue
		}
		cfg.conectados[indice] = false
	}
}

// Comprobar estado remoto de un nodo con respecto a un estado prefijado
func (cfg *configDespliegue) comprobarEstadoRemoto(idNodoDeseado int,
	mandatoDeseado int, esLiderDeseado bool, IdLiderDeseado int) {
	idNodo, mandato, esLider, idLider := cfg.obtenerEstadoRemoto(idNodoDeseado)

	cfg.t.Log("Estado replica 0: ", idNodo, mandato, esLider, idLider, "\n")

	if idNodo != idNodoDeseado || mandato != mandatoDeseado ||
		esLider != esLiderDeseado || idLider != IdLiderDeseado {
		cfg.t.Fatalf("Estado incorrecto en replica %d en subtest %s",
			idNodoDeseado, cfg.t.Name())
	}

}

func (cfg *configDespliegue) detenerReplica(indice int) {
	if !cfg.conectados[indice] {
		return
	}
	var reply raft.Vacio
	err := cfg.nodosRaft[indice].CallTimeout("NodoRaft.ParaNodo",
		raft.Vacio{}, &reply, 100*time.Millisecond)
	if err != nil && cfg.t != nil {
		cfg.t.Logf("Aviso al detener réplica %d: %v", indice, err)
	}
	cfg.conectados[indice] = false
	time.Sleep(500 * time.Millisecond)
}

func (cfg *configDespliegue) arrancarReplica(indice int) {
	if cfg.conectados[indice] {
		return
	}
	comando := fmt.Sprintf("%s %d %s", EXECREPLICACMD, indice,
		rpctimeout.HostPortArrayToString(cfg.nodosRaft))
	despliegue.ExecMutipleHosts(comando,
		[]string{cfg.nodosRaft[indice].Host()}, cfg.cr, PRIVKEYFILE)
	time.Sleep(3 * time.Second)
	cfg.conectados[indice] = true
}

func (cfg *configDespliegue) contarConectados() int {
	total := 0
	for _, conectado := range cfg.conectados {
		if conectado {
			total++
		}
	}
	return total
}

func (cfg *configDespliegue) esperarLiderConMayoria(timeout time.Duration) (int, bool) {
	mayoria := cfg.numReplicas/2 + 1
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if cfg.contarConectados() < mayoria {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		lider := -1
		for i := 0; i < cfg.numReplicas; i++ {
			if !cfg.conectados[i] {
				continue
			}
			_, _, esLider, _ := cfg.obtenerEstadoRemoto(i)
			if esLider {
				if lider != -1 && lider != i {
					return -1, false
				}
				lider = i
			}
		}
		if lider != -1 {
			return lider, true
		}
		time.Sleep(200 * time.Millisecond)
	}

	return -1, false
}

func (cfg *configDespliegue) someterOperacionRaft(idNodo int,
	operacion raft.TipoOperacion) raft.ResultadoRemoto {
	var reply raft.ResultadoRemoto
	err := cfg.nodosRaft[idNodo].CallTimeout("NodoRaft.SometerOperacionRaft",
		operacion, &reply, 100*time.Millisecond)
	check.CheckError(err, "Error en llamada RPC SometerOperacionRaft")

	return reply
}

func (cfg *configDespliegue) someterOperacionRaftNoFatal(idNodo int,
	operacion raft.TipoOperacion) (raft.ResultadoRemoto, error) {
	var reply raft.ResultadoRemoto
	err := cfg.nodosRaft[idNodo].CallTimeout("NodoRaft.SometerOperacionRaft",
		operacion, &reply, 100*time.Millisecond)

	return reply, err
}
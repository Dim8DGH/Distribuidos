#!/bin/bash

# Este script automatiza las pruebas de un clúster Raft desplegado en Kubernetes.
# Detiene la ejecución si cualquier comando falla.
set -e

# --- Configuración ---
CLIENT_IMAGE="localhost:5001/raft-client:latest"
RAFT_NODES=(
  "raft-0.raft-service.default.svc.cluster.local:29000"
  "raft-1.raft-service.default.svc.cluster.local:29000"
  "raft-2.raft-service.default.svc.cluster.local:29000"
)

# --- Funciones Auxiliares ---
#
mostrarEstado() {
  status_output=$(run_client status "${RAFT_NODES[@]}")
  echo $status_output

}

# Función para ejecutar el cliente de forma no interactiva y capturar la salida.
run_client() {
    # Creamos un pod con un nombre único para evitar colisiones
    local pod_name="clt-test-$(date +%s)"
    
    # Ejecutamos el pod y esperamos a que se cree
    kubectl run "${pod_name}" --image=${CLIENT_IMAGE} --image-pull-policy=Always --restart=Never -- "$@" >/dev/null
    
    # Esperamos a que el pod complete su ejecución
    local attempts=0
    local max_attempts=60 # Timeout de 60 segundos
    
    while true; do
        # Obtenemos el estado del pod. Si no existe, seguimos esperando.
        local status_phase=$(kubectl get pod "${pod_name}" -o jsonpath='{.status.phase}' 2>/dev/null || echo "NotFound")
        
        if [ "$status_phase" == "Succeeded" ] || [ "$status_phase" == "Failed" ]; then
            break # El pod ha terminado
        fi
        
        if [ $attempts -ge $max_attempts ]; then
            echo "Error: El pod cliente ${pod_name} tardó demasiado en completar o se quedó atascado en estado ${status_phase}." >&2
            kubectl delete pod "${pod_name}" --ignore-not-found=true > /dev/null
            return 1
        fi
        
        sleep 1
        attempts=$((attempts+1))
    done

    # Si el pod falló, mostramos los logs y el error
    if [ "$status_phase" == "Failed" ]; then
        echo "Error: El pod cliente ${pod_name} falló." >&2
        kubectl logs "${pod_name}" >&2
        kubectl delete pod "${pod_name}" --ignore-not-found=true > /dev/null
        return 1
    fi
    
    # Obtenemos los logs y los mostramos para el que llama
    kubectl logs "${pod_name}"
    
    # Limpiamos el pod
    kubectl delete pod "${pod_name}" --ignore-not-found=true > /dev/null
}

# Función para comprobar el estado del clúster y encontrar un líder.
# Reintenta varias veces para dar tiempo a la elección del líder.
check_leader() {
  # Imprime los mensajes de progreso a stderr para no "contaminar" la salida que capturamos
  echo "--- Verificando estado del clúster y buscando un líder..." >&2
  local attempts=0
  local max_attempts=12
  local status_output=""

  while [ $attempts -lt $max_attempts ]; do
    status_output=$(run_client status "${RAFT_NODES[@]}")
    
    if [[ "$status_output" == *"Lider"* ]]; then
      echo "Líder encontrado." >&2
      # Envía SÓLO la PRIMERA línea del líder a stdout, para que el comando que llama la capture
      echo "$status_output" | grep "Lider" | head -n 1
      return 0 # Éxito
    else
      echo "Aún no hay líder, esperando 5 segundos... (Intento $((attempts+1))/$max_attempts)" >&2
      sleep 5
      attempts=$((attempts+1))
    fi
  done

  echo "Error: No se pudo encontrar un líder después de $max_attempts intentos." >&2
  exit 1
}

# --- Ejecución de las Pruebas ---

echo "===== INICIANDO PRUEBAS AUTOMÁTICAS DEL CLÚSTER RAFT ====="

# 1. Chequeo inicial del estado del clúster
echo -e "\n--- Paso 1: Chequeo inicial del estado del clúster ---"
leader_line=$(check_leader)
mostrarEstado
if [ -z "$leader_line" ]; then exit 1; fi

# 2. Escribir un valor
echo -e "\n--- Paso 2: Prueba de escritura (PUT)..."
run_client put autotest-key "valor-$(date +%s)" "${RAFT_NODES[@]}"
echo "Comando PUT ejecutado."

# 3. Leer el valor
echo -e "\n--- Paso 3: Prueba de lectura (GET)..."
run_client get autotest-key "${RAFT_NODES[@]}"
echo "Comando GET ejecutado. Revisa los logs de los servidores para ver el valor."
sleep 3

# 4. Prueba de tolerancia a fallos
echo -e "\n--- Paso 4: Prueba de tolerancia a fallos (eliminación del líder)..."

# Extrae el nombre del pod del líder de la línea de estado
leader_dns=$(echo "$leader_line" | awk '{print $3}' | tr -d '()')
leader_pod=$(echo "$leader_dns" | cut -d'.' -f1)

if [ -z "$leader_pod" ]; then
  echo "Error: No se pudo identificar el pod del líder." >&2
  exit 1
fi
echo "Líder actual es el pod: $leader_pod"

# Eliminar el pod del líder
echo "Eliminando el pod del líder ($leader_pod)..."
# Usamos --grace-period=0 para forzar la eliminación, simulando un fallo abrupto
kubectl delete pod "$leader_pod" --grace-period=0 --force --ignore-not-found=true
echo "Pod eliminado. Esperando 30 segundos a que Kubernetes lo recree y el clúster se recupere..."
sleep 30

# Verificar que el clúster se ha recuperado y hay un nuevo líder
echo "Verificando la recuperación del clúster..."
check_leader

# 5. Verificar que el estado se mantuvo
echo -e "\n--- Paso 5: Verificando la persistencia del estado tras el fallo..."
run_client get autotest-key "${RAFT_NODES[@]}"
echo "Comando GET ejecutado después de la recuperación."
echo "Revisa los logs de los servidores para confirmar que el valor se ha conservado."

echo -e "\n===== PRUEBAS AUTOMÁTICAS COMPLETADAS CON ÉXITO ====="

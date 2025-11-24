#!/bin/bash

if [ $# -ne 1 ]; then
  echo "Uso: $0 machines.txt"
  exit 1
fi

FICHERO="$1"

while IFS=: read -r ip port user exepath; do
  if [[ -z "$ip" || -z "$port" || -z "$user" || -z "$exepath" ]]; then
    echo "Saltando línea mal formada: $ip:$port:$user:$exepath"
    continue
  fi
  echo "Conectando a $user@$ip y ejecutando '$exepath $port'"
  ssh "$user@$ip" "$exepath $port" &
done < "$FICHERO"

sleep 5
echo "Esperando a que arranque el programa en las maquinas"

./master/master machines_master.txt

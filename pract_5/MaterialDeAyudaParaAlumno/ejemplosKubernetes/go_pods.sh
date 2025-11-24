kubectl delete pod s1
kubectl delete pod c1
kubectl delete pod c2
kubectl delete service prueba
echo "--------- Esperar un poco para dar tiempo que terminen Pods previos"
sleep 1
kubectl create -f pods_go.yaml

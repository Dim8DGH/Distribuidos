* Crear cluster kubernetes con "kind"

    - ./kind-with-registry.sh     # con registro local de contenedores
    
    - Crear cluster de nombre kind:
            kind-with-registry.sh
            ./kind create cluster --config kind-config.yaml
    - Suprimir cluster de nombre "kind":
            ./kind delete cluster
    - Obtener cluster ejecutandose en kind:
            ./kind get clusters
    - Obtener logs de kind:
            kind export logs ./somedir

    
* Por defecto, configuración de acceso al cluster, con kubectl, se almacena en
    - ${HOME}/.kube/config
    
* kubectl : cheat sheet disponible
    - kubectl cluster-info --context kind-kind  # resumen muy básico
    - kubectl cluster-info dump     # depuracion y diagnostico del cluster
    - kubectl get all --all-namespaces -o wide
    - kubectl exec e2 -ti -- sh
    - kubectl logs e1
    
* Golang static build :
    - CGO_ENABLED=0 go build
    
 * Comandos docker
    - docker build . -t localhost:5000/servidor:latest
    - docker push localhost:5000/servidor:latest   # al registro kind-registry
												   # puesto en marcha por "kind"
    - docker run -it --rm -p 6000:6000 localhost:5000/servidor:latest
    - docker ps -a -s   # contenedores en marcha y parados, incluido tamaño
    - docker stop <container id or name>
    - docker images
    - docker rm <container id or name>
    - docker rmi <image id>
    - docker push
    
* docker registry
    - Stop registry and remove all data (https://docs.docker.com/registry/)
        - docker container stop kind-registry
        - docker container rm -v kind-registry
    

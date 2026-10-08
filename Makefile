BINARY := bin/csi-demo
IMAGE  := csi-demo:dev
CLUSTER := csi-demo

.PHONY: build test vet fmt image cluster-create cluster-delete deploy clean

build: ## Build the driver binary
	go build -o $(BINARY) ./cmd/csi-demo

test: ## Run unit tests
	go test ./...

vet: ## Run go vet
	go vet ./...

fmt: ## Format sources
	gofmt -l -w .

image: build ## Build the container image
	docker build -t $(IMAGE) .

cluster-create: ## Create the dedicated kind cluster
	kind create cluster --config deploy/kind-cluster.yaml

cluster-delete: ## Delete the dedicated kind cluster
	kind delete cluster --name $(CLUSTER)

deploy: ## Build, load the image into kind, and apply manifests
	$(MAKE) image
	kind load docker-image $(IMAGE) --name $(CLUSTER)
	kubectl apply -f deploy/csidriver.yaml
	kubectl apply -f deploy/node.yaml

clean:
	rm -rf bin
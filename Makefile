start:
	docker compose up -d
	go build main.go
	./main start

stop:
	./main stop
	docker compose down

logs:
	./main logs

DIND_IMAGE ?= asdozzz/dind-unit:latest
DIND_DOCKERFILE ?= Dockerfile.Dind

dind-build:
	docker build -f $(DIND_DOCKERFILE) -t $(DIND_IMAGE) .

dind-push:
	docker push $(DIND_IMAGE)

# Собрать и отправить в Docker Hub dind-образ с OpenCode.
dind: dind-build dind-push

# Совместимый алиас для существующих скриптов и IDE-конфигураций.
dind-unit: dind

podman-unit:
	docker build -f Dockerfile.dist -t asdozzz/unitman-unit:latest . && docker push asdozzz/unitman-unit:latest
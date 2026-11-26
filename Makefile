start:
	docker compose up -d
	go build -o main .
	./main start

stop:
	./main stop
	docker compose down

logs:
	./main logs

DIND_IMAGE ?= unitman/unit:latest
DIND_DEV_IMAGE ?= unitman/unit:dev
DIND_DOCKERFILE ?= Dockerfile.Dind

IDE_IMAGE ?= unitman/ide:latest
IDE_DEV_IMAGE ?= unitman/ide:dev
IDE_CONTEXT ?= temporal/activity/unit/docker-dind/ide
IDE_DOCKERFILE ?= $(IDE_CONTEXT)/Dockerfile

dind-build:
	docker build -f $(DIND_DOCKERFILE) -t $(DIND_IMAGE) .

dind-dev:
	docker build -f $(DIND_DOCKERFILE) -t $(DIND_DEV_IMAGE) .

dind-push:
	docker push $(DIND_IMAGE)

ide-build:
	docker build -f $(IDE_DOCKERFILE) -t $(IDE_IMAGE) $(IDE_CONTEXT)

ide-dev:
	docker build -f $(IDE_DOCKERFILE) -t $(IDE_DEV_IMAGE) $(IDE_CONTEXT)

ide-push:
	docker push $(IDE_IMAGE)

# Собрать и отправить unit-образ с OpenCode в реестр установки.
dind: dind-build dind-push

# Собрать все предсобранные образы юнита локально для разработки.
images-dev: dind-dev ide-dev

# Собрать и отправить все предсобранные образы юнита.
images-push: dind-build dind-push ide-build ide-push

# Совместимый алиас для существующих скриптов и IDE-конфигураций.
dind-unit: dind
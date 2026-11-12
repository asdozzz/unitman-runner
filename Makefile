start:
	docker compose up -d
	go build main.go
	nohup ./main &
stop:
	docker compose down
	kill -9 `pidof ./main` || true
	rm -rf nohup.out

dind-unit:
	docker build -f DockerfileDind.dist -t asdozzz/dind-unit:latest . && docker push asdozzz/dind-unit:latest

podman-unit:
	docker build -f Dockerfile.dist -t asdozzz/unitman-unit:latest . && docker push asdozzz/unitman-unit:latest

clear-registry:
	sudo rm -rf ./register_data
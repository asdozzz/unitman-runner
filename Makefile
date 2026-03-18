start:
	docker compose up -d
	go build main.go
	nohup ./main &
stop:
	docker compose down
	kill -9 `pidof ./main` || true
	rm -rf nohup.out
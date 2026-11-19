#!/bin/sh
set -e

# Удаляем флаг готовности на случай рестарта контейнера
rm -f /tmp/.app_ready

# 1. Запускаем dockerd в фоне
rm -f /var/run/docker.pid /var/run/docker.sock
dockerd \
  --host=unix:///var/run/docker.sock \
  --registry-mirror=http://unitman-registry:5000 \
  --insecure-registry=unitman-registry:5000 \
  --storage-driver=overlay2 \
  --max-concurrent-downloads=10 \
  --max-concurrent-uploads=10 \
  --mtu=1400 >/var/log/dockerd.log 2>&1 &

# 2. Готовим каталог приложения. Содержимое живёт в именованном томе.
mkdir -p /app

# 3. Сигнализируем healthcheck, что контейнер готов к работе
touch /tmp/.app_ready

# 4. Ждем сигнал остановки
while true; do sleep 1 & wait $!; done

#!/bin/sh
set -e

# Флаг готовности снимается на случай рестарта контейнера.
rm -f /tmp/.workspace_ready

# 1. dockerd для вложенных контейнеров проекта.
rm -f /var/run/docker.pid /var/run/docker.sock
dockerd \
  --host=unix:///var/run/docker.sock \
  --registry-mirror=http://unitman-registry:5000 \
  --insecure-registry=unitman-registry:5000 \
  --storage-driver=overlay2 \
  --max-concurrent-downloads=10 \
  --max-concurrent-uploads=10 \
  --mtu=1400 >/var/log/dockerd.log 2>&1 &

# 2. Каталоги, которые живут в именованных томах.
mkdir -p /workspace
mkdir -p /root/.local/share/opencode

# 3. Сообщаем healthcheck, что workspace готов.
touch /tmp/.workspace_ready

# 4. Ждём сигнал остановки.
while true; do sleep 1 & wait $!; done

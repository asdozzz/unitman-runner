#!/bin/sh
set -e

# Реестр образов выдаётся backend-ом при регистрации раннера.
# Канонический формат UNITMAN_REGISTRY_HOST — абсолютный URL
# scheme://host[:port] без завершающего слэша. Допускается host[:port],
# тогда используется схема http. Fallback на старые имена запрещён.
registry_host="${UNITMAN_REGISTRY_HOST:-}"
registry_host="${registry_host%/}"
case "$registry_host" in
  "")
    echo "UNITMAN_REGISTRY_HOST не задан: укажите адрес реестра, полученный при регистрации" >&2
    exit 1
    ;;
  *://*)
    ;;
  *)
    registry_host="http://${registry_host}"
    ;;
esac
registry_authority="${registry_host#*://}"
if [ -z "$registry_authority" ]; then
  echo "UNITMAN_REGISTRY_HOST содержит пустой хост: ${UNITMAN_REGISTRY_HOST}" >&2
  exit 1
fi

# Удаляем флаг готовности на случай рестарта контейнера
rm -f /tmp/.app_ready

# 1. Запускаем dockerd в фоне
rm -f /var/run/docker.pid /var/run/docker.sock
dockerd \
  --host=unix:///var/run/docker.sock \
  --registry-mirror="${registry_host}" \
  --insecure-registry="${registry_authority}" \
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

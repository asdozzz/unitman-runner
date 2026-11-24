# Read-only VS Code для файлов юнита.
# Базовый образ зафиксирован тегом, чтобы поведение не менялось неявно.
FROM codercom/code-server:4.93.1

# Cleanup раннера чистит неиспользуемые образы (`docker image prune -af`).
# Метка защищает предсобранный IDE-образ от удаления между запусками.
LABEL unitman.keep=true

USER root

# git нужен для git diff по read-only тому; wget используется для healthcheck.
RUN apt-get update \
    && apt-get install -y --no-install-recommends git ca-certificates wget \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /home/coder/project /opt/unitman-ide \
    && chown -R coder:coder /home/coder/project

# settings.json кладётся в read-only каталог образа и копируется entrypoint-ом
# в /tmp, потому что корневая ФС контейнера смонтирована read_only.
# settings.edit.json используется в режиме редактора: без readonlyInclude,
# с включённым терминалом.
COPY settings.json /opt/unitman-ide/settings.json
COPY settings.edit.json /opt/unitman-ide/settings.edit.json
COPY entrypoint.sh /usr/local/bin/unitman-ide-entrypoint.sh

RUN chmod +x /usr/local/bin/unitman-ide-entrypoint.sh

USER coder
WORKDIR /home/coder/project

EXPOSE 47000

ENTRYPOINT ["/usr/local/bin/unitman-ide-entrypoint.sh"]

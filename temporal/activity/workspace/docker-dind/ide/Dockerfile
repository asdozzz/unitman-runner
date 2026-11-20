# Multi-root IDE для workspace. Образ зафиксирован тегом.
FROM codercom/code-server:4.93.1

USER root

# git нужен для diff/status, wget — для healthcheck.
RUN apt-get update \
    && apt-get install -y --no-install-recommends git ca-certificates wget \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /home/coder/workspace /opt/unitman-ide \
    && chown -R coder:coder /home/coder/workspace

COPY settings.json /opt/unitman-ide/settings.json
COPY entrypoint.sh /usr/local/bin/unitman-workspace-ide-entrypoint.sh

RUN chmod +x /usr/local/bin/unitman-workspace-ide-entrypoint.sh

USER coder
WORKDIR /home/coder/workspace

EXPOSE 47000

ENTRYPOINT ["/usr/local/bin/unitman-workspace-ide-entrypoint.sh"]

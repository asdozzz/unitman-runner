FROM asdozzz/dind-unit:latest

# git нужен для клонирования и обновления проектов workspace.
RUN apk add --no-cache git

COPY entrypoint.sh /usr/local/bin/workspace-entrypoint.sh
COPY workspace-repo-sync.sh /usr/local/bin/workspace-repo-sync

RUN chmod +x \
    /usr/local/bin/workspace-entrypoint.sh \
    /usr/local/bin/workspace-repo-sync

ENTRYPOINT ["/usr/local/bin/workspace-entrypoint.sh"]

FROM quay.io/podman/stable

RUN yum -y install gettext
RUN dnf install -y podman-compose

ENTRYPOINT ["tail"]
CMD ["-f","/dev/null"]

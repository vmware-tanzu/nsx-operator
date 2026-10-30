FROM golang:1.26.8 AS golang-build

WORKDIR /source
COPY . /source
RUN CGO_ENABLED=0 go build -o supervisor-network-migration-operator ./cmd_migration

FROM photon

RUN tdnf -y install shadow && \
    useradd -s /bin/bash migration-operator && \
    tdnf clean all && \
    rm -rf /var/cache/tdnf

COPY --from=golang-build /source/supervisor-network-migration-operator /usr/local/bin/

USER migration-operator
ENTRYPOINT ["/usr/local/bin/supervisor-network-migration-operator"]

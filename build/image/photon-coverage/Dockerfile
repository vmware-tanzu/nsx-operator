FROM golang:1.26.8 as golang-build

WORKDIR /source

COPY . /source
RUN CGO_ENABLED=0 go build -o manager -covermode atomic cmd/main.go
RUN CGO_ENABLED=0 go build -o clean -covermode atomic cmd_clean/main.go
RUN CGO_ENABLED=0 go build -o supervisor-network-migration-operator -covermode atomic ./cmd_migration

FROM photon

RUN tdnf -y install shadow && \
    useradd -s /bin/bash nsx-operator

COPY --from=golang-build /source/manager /usr/local/bin/
COPY --from=golang-build /source/clean /usr/local/bin/
COPY --from=golang-build /source/supervisor-network-migration-operator /usr/local/bin/

USER nsx-operator

ENTRYPOINT ["/usr/local/bin/manager"]

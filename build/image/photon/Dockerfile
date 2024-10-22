FROM golang:1.22.7 as golang-build

WORKDIR /source

COPY . /source
RUN CGO_ENABLED=0 go build -o manager cmd/main.go
RUN CGO_ENABLED=0 go build -o clean cmd_clean/main.go

FROM photon

RUN tdnf -y install shadow && \
    useradd -s /bin/bash nsx-operator

COPY --from=golang-build /source/manager /usr/local/bin/
COPY --from=golang-build /source/clean /usr/local/bin/

USER nsx-operator

ENTRYPOINT ["/usr/local/bin/manager"]

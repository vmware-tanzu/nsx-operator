FROM golang:1.19.5 as golang-build

WORKDIR /source

COPY . /source

RUN go build -o manager cmd/main.go


FROM photon

RUN tdnf -y install shadow && \
    useradd -s /bin/bash nsx-operator

COPY --from=golang-build /source/manager /usr/local/bin/

USER nsx-operator

ENTRYPOINT ["/usr/local/bin/manager"]
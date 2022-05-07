FROM golang:1.17.7 as golang-build

WORKDIR /source

COPY . /source

RUN go build -o manager cmd/main.go


FROM photon

RUN tdnf -y install shadow && \
    useradd -s /bin/bash nsx-operator && \
    mkdir /manifests

COPY --from=golang-build /source/manager /usr/local/bin/
COPY --from=golang-build /source/build/yaml/prometheus /manifests/

USER nsx-operator

ENTRYPOINT ["/usr/local/bin/manager"]
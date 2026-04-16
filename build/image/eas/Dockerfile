FROM gobuild-container-registry.lvn.broadcom.net/cayman_golang/vmware-go1.25-unified/7133ba4ccd4f1aaa0e4997ff8256b6fbc220c8d8/release/publish/infra/docker/boring-photon5-amd64/1.25.8:ob-25280203 as golang-build

WORKDIR /source

COPY . /source
RUN CGO_ENABLED=0 go build -o eas cmd_eas/main.go

FROM photon

RUN tdnf -y install shadow && \
    useradd -s /bin/bash nsx-eas

COPY --from=golang-build /source/eas /usr/local/bin/

USER nsx-operator

ENTRYPOINT ["/usr/local/bin/eas"]

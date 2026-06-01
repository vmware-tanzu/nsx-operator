FROM golang:1.25.7 as golang-build

WORKDIR /source

COPY . /source
RUN CGO_ENABLED=0 go build -o eas cmd_eas/main.go

FROM photon

RUN tdnf -y install shadow && \
    useradd -s /bin/bash eas

COPY --from=golang-build /source/eas /usr/local/bin/

USER eas

ENTRYPOINT ["/usr/local/bin/eas"]

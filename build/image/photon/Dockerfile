FROM golang:1.16 as golang-build

WORKDIR /source

COPY . /source

RUN go build -o manager cmd/main.go


FROM photon

COPY --from=golang-build /source/manager /usr/local/bin/

ENTRYPOINT [ "/usr/local/bin/manager"]

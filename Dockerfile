# FROM golang:1.19 as go
# WORKDIR /builder
# ENV CGO_ENABLED=0
# COPY main.go go.mod go.sum ./
# COPY pkg ./pkg
# RUN go build -a -installsuffix cgo -o /monitor main.go

# FROM scratch
# COPY --from=go /monitor /monitor
# ENTRYPOINT [ "/monitor" ]

FROM scratch
COPY bin/monitor /monitor
ENTRYPOINT [ "/monitor" ]
LABEL org.opencontainers.image.source https://github.com/galleybytes/monitor
FROM golang:1.26.5 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /out/sshgate ./cmd/sshgate

FROM gcr.io/distroless/static
COPY --from=build /out/sshgate /sshgate
ENTRYPOINT ["/sshgate"]

FROM golang:1.26.5 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /out/ssh-mcp ./cmd/ssh-mcp

FROM gcr.io/distroless/static
COPY --from=build /out/ssh-mcp /ssh-mcp
ENTRYPOINT ["/ssh-mcp"]

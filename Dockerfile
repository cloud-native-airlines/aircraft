FROM golang:1.27 AS build
WORKDIR /src

# Module files first for layer caching. (No third-party deps yet, so no go.sum.)
COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/flight ./cmd/flight

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/flight /flight
COPY scenarios /scenarios
ENV FLIGHT_PLAN=/scenarios/msp-ord.json \
    LISTEN_ADDR=0.0.0.0:8080
EXPOSE 8080
ENTRYPOINT ["/flight"]

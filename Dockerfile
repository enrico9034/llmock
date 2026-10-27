FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /llmock ./cmd/llmock

FROM gcr.io/distroless/static:nonroot
COPY --from=build /llmock /llmock
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/llmock"]
CMD ["serve"]

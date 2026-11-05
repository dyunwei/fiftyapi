.PHONY: build web test test-ui clean release test-load

web:
	cd web && npm ci --no-audit --no-fund && npm run build

build: web
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -buildvcs=false -o bin/fiftyapi ./cmd/fiftyapi

test:
	go test -race -timeout=3m ./...
	npm --prefix web run test:unit
	go vet ./...
	python3 scripts/test_export_sub2api.py
	python3 scripts/test_migrate_sub2api.py
	python3 scripts/check-source.py

test-ui: build
	cd web && FIFTYAPI_TEST_BINARY="$(CURDIR)/bin/fiftyapi" npx playwright test

clean:
	rm -rf bin dist web/test-results web/playwright-report

release:
	./scripts/build-release.sh

test-load:
	go test -count=1 -run TestAntigravityConcurrentGatewayLoad -v ./internal/gateway

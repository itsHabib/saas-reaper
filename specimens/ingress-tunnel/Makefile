.PHONY: setup check demo invariants deploy-check verify

setup:
	./scripts/setup.sh

check:
	./scripts/check.sh

demo:
	./scripts/demo.sh

invariants:
	./scripts/invariants.sh

deploy-check:
	./scripts/deploy-check.sh

verify: check demo invariants deploy-check

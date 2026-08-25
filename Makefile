.PHONY: setup demo check

setup:
	./scripts/setup.sh

demo: setup
	./scripts/demo.sh

check: setup
	./scripts/check.sh

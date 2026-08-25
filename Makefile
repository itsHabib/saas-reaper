.PHONY: setup demo product-demo check verify

setup:
	./scripts/setup.sh

demo: setup
	./scripts/demo.sh

product-demo: setup
	./scripts/product-demo.sh

check: setup
	./scripts/check.sh

verify: check product-demo

.PHONY: setup demo product-demo check

setup:
	./scripts/setup.sh

demo: setup
	./scripts/demo.sh

product-demo: setup
	./scripts/product-demo.sh

check: setup
	./scripts/check.sh

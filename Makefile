.PHONY: setup work demo product-demo tunnel-demo tunnel-invariants tunnel-deploy-check tunnel-proof check verify

setup:
	./scripts/setup.sh

work:
	@bash scripts/check-work.sh --json

demo: setup
	./scripts/demo.sh

product-demo: setup
	./scripts/product-demo.sh

tunnel-demo:
	$(MAKE) -C specimens/ingress-tunnel demo

tunnel-invariants:
	$(MAKE) -C specimens/ingress-tunnel invariants

tunnel-deploy-check:
	$(MAKE) -C specimens/ingress-tunnel deploy-check

tunnel-proof: check tunnel-demo tunnel-invariants tunnel-deploy-check

check: setup
	./scripts/check.sh

verify: product-demo tunnel-proof

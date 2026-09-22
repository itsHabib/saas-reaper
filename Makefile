.PHONY: setup work demo product-demo webhook-demo webhook-invariants webhook-proof incident-demo incident-invariants incident-proof check verify tunnel-demo tunnel-invariants tunnel-deploy-check tunnel-proof

setup:
	./scripts/setup.sh

work:
	@bash scripts/check-work.sh --json

demo: setup
	./scripts/demo.sh

product-demo: setup
	./scripts/product-demo.sh

webhook-demo: setup
	$(MAKE) -C specimens/webhook-delivery demo

webhook-invariants: setup
	$(MAKE) -C specimens/webhook-delivery invariants

webhook-proof: check webhook-demo webhook-invariants

incident-demo:
	$(MAKE) -C specimens/incident-escalation demo

incident-invariants:
	$(MAKE) -C specimens/incident-escalation invariants

incident-proof: check incident-demo incident-invariants

tunnel-demo: setup
	$(MAKE) -C specimens/ingress-tunnel demo

tunnel-invariants: setup
	$(MAKE) -C specimens/ingress-tunnel invariants

tunnel-deploy-check: setup
	$(MAKE) -C specimens/ingress-tunnel deploy-check

tunnel-proof: check tunnel-demo tunnel-invariants tunnel-deploy-check

check: setup
	./scripts/check.sh

verify: product-demo webhook-proof incident-demo incident-invariants tunnel-proof

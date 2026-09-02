.PHONY: setup work demo product-demo webhook-demo webhook-invariants webhook-proof audit-demo audit-invariants audit-proof check verify

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

audit-demo:
	$(MAKE) -C specimens/audit-ledger demo

audit-invariants:
	$(MAKE) -C specimens/audit-ledger invariants

audit-proof: check audit-demo audit-invariants

check: setup
	./scripts/check.sh

verify: product-demo webhook-proof audit-demo audit-invariants

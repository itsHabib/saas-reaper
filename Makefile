.PHONY: setup work demo product-demo webhook-demo webhook-invariants webhook-proof \
	notification-demo notification-invariants notification-proof check verify

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

notification-demo:
	$(MAKE) -C specimens/notification-routing demo

notification-invariants:
	$(MAKE) -C specimens/notification-routing invariants

notification-proof: check notification-demo notification-invariants

check: setup
	./scripts/check.sh

verify: product-demo webhook-proof notification-proof

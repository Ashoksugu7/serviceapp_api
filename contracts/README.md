# API contract

[openapi.json](openapi.json) is the OpenAPI 3.1 target contract for Phase 1. It includes all 60 v3 operations plus four operational health operations. It is a specification, not an implementation-status report. See [T01 decisions](../docs/t01-requirements.md) for tenant/state rules and [readable contract](../docs/api-contract-draft.md) for a summary.

## Validate

Use an isolated Python environment; these checks do not compile or start the Go application:

```sh
python3 -m venv /tmp/serviceops-contract-validation
/tmp/serviceops-contract-validation/bin/python -m pip install -r api/tests/validation-requirements.txt
/tmp/serviceops-contract-validation/bin/python api/tests/validate_contract.py
```

Run from the repository root. The checks verify the OpenAPI document, route inventory against the v3 specification, operation IDs, role restrictions, bodyless HEAD responses and 38 schema acceptance/rejection cases. Runtime tenant isolation, database relationships, formulas and workflows still require implementation tests.

The specification uses the [OpenAPI 3.1 standard](https://spec.openapis.org/oas/v3.1.0.html). Update schemas, decision documentation and acceptance cases together when a contract changes.

# Sluice v1 build ledger

status: IN_PROGRESS
sdd_sha256: da422836d9c5d45d47c51c922e16ca0acfac0a3288d59d76c87f25d4f5272fbc
current_slice: S3
review_round: 0
review_complete: false
blocking_findings_open: 0

## Blocked

## Items

| ID | Status | Slice | Evidence |
|---|---|---|---|
| REQ-API-001 | PASS | S0 | SCN-API-001 |
| REQ-API-002 | PASS | S0 | SCN-API-002 |
| REQ-API-003 | IN_PROGRESS | S0 | |
| REQ-API-004 | IN_PROGRESS | S0 | |
| REQ-CORE-001 | IN_PROGRESS | S0 | |
| REQ-CORE-002 | IN_PROGRESS | S0 | |
| REQ-CORE-003 | PASS | S0 | SCN-CORE-002 |
| REQ-CORE-004 | PASS | S0 | SCN-CORE-003 |
| REQ-CORE-005 | IN_PROGRESS | S0 | |
| REQ-CORE-006 | IN_PROGRESS | S0 | |
| REQ-CORE-007 | PASS | S0 | SCN-CORE-006 |
| REQ-CORE-008 | IN_PROGRESS | S0 | |
| REQ-CORE-009 | IN_PROGRESS | S0 | |
| REQ-CORE-010 | IN_PROGRESS | S0 | |
| REQ-DOC-001 | PASS | S0 | SCN-DOC-001 |
| REQ-UI-001 | IN_PROGRESS | S0 | |
| REQ-UI-010 | IN_PROGRESS | S0 | |
| SCN-API-001 | PASS | S0 | internal/api/gen_integration_test.go::TestSCN_API_001_RegenerateNoDiff |
| SCN-API-002 | PASS | S0 | tests/e2e/auth_test.go::TestSCN_API_002_ErrorEnvelope |
| SCN-API-003 | IN_PROGRESS | S0 | |
| SCN-API-004 | IN_PROGRESS | S0 | |
| SCN-CORE-001 | IN_PROGRESS | S0 | |
| SCN-CORE-002 | PASS | S0 | internal/platform/db/db_integration_test.go::TestSCN_CORE_002_ConcurrentMigrations |
| SCN-CORE-003 | PASS | S0 | tests/e2e/storage_test.go::TestSCN_CORE_003_HealthReadyMetrics |
| SCN-CORE-004 | IN_PROGRESS | S0 | |
| SCN-CORE-005 | PASS | S0 | internal/platform/lease/lease_integration_test.go::TestSCN_CORE_005_LeaseHandover |
| SCN-CORE-006 | PASS | S0 | tests/e2e/instances_test.go::TestSCN_CORE_006_InstancesRegistry |
| SCN-CORE-007 | IN_PROGRESS | S0 | |
| SCN-CORE-008 | IN_PROGRESS | S0 | |
| SCN-DOC-001 | PASS | S0 | internal/app/config_test.go::TestSCN_DOC_001_EnvDocGenerated; internal/flow/fixtures_test.go::TestSCN_DOC_001_FlowDocGenerated |
| SCN-UI-001 | IN_PROGRESS | S0 | |
| REQ-AUTH-001 | PASS | S1 | SCN-AUTH-001 |
| REQ-AUTH-002 | PASS | S1 | SCN-AUTH-002 |
| REQ-AUTH-003 | PASS | S1 | SCN-AUTH-003; SCN-AUTH-004 |
| REQ-AUTH-004 | PASS | S1 | SCN-AUTH-007 |
| REQ-AUTH-005 | IN_PROGRESS | S1 |  |
| REQ-AUTH-006 | IN_PROGRESS | S1 |  |
| REQ-AUTH-007 | IN_PROGRESS | S1 |  |
| REQ-AUTH-008 | PASS | S1 | SCN-AUTH-008 |
| REQ-AUTH-009 | PASS | S1 | SCN-AUTH-009 |
| REQ-UI-002 | IN_PROGRESS | S1 |  |
| SI-02 | PASS | S1 | SCN-AUTH-012 |
| SI-03 | PASS | S1 | SCN-AUTH-006 |
| SI-06 | PASS | S1 | SCN-AUTH-011 |
| SI-11 | PASS | S1 | SCN-AUTH-008 |
| SI-12 | PASS | S1 | SCN-AUTH-013 |
| SCN-AUTH-001 | PASS | S1 | tests/ui/specs/auth.spec.ts::SCN-AUTH-001 login errors, reload, logout and old cookie |
| SCN-AUTH-002 | PASS | S1 | tests/e2e/auth_test.go::TestSCN_AUTH_002_BootstrapAdminAndCLI |
| SCN-AUTH-003 | PASS | S1 | tests/ui/specs/auth.spec.ts::SCN-AUTH-003 admin creates an editor who must set a new password |
| SCN-AUTH-004 | PASS | S1 | tests/e2e/auth_test.go::TestSCN_AUTH_004_LastAdminGuard |
| SCN-AUTH-005 | IN_PROGRESS | S1 |  |
| SCN-AUTH-006 | PASS | S1 | internal/app/routes_integration_test.go::TestSCN_AUTH_006_RouteInventory |
| SCN-AUTH-007 | PASS | S1 | tests/e2e/auth_test.go::TestSCN_AUTH_007_ChangePassword |
| SCN-AUTH-008 | PASS | S1 | tests/e2e/auth_test.go::TestSCN_AUTH_008_LoginRateLimit |
| SCN-AUTH-009 | PASS | S1 | tests/e2e/auth_test.go::TestSCN_AUTH_009_DisableTakesEffectOnAllInstances |
| SCN-AUTH-010 | IN_PROGRESS | S1 |  |
| SCN-AUTH-011 | PASS | S1 | tests/e2e/auth_test.go::TestSCN_AUTH_011_SameOriginForCookies |
| SCN-AUTH-012 | PASS | S1 | internal/auth/auth_integration_test.go::TestSCN_AUTH_012_SecretsStoredAsHashes |
| SCN-AUTH-013 | PASS | S1 | tests/e2e/auth_test.go::TestSCN_AUTH_013_SecurityHeaders |
| SCN-UI-005 | IN_PROGRESS | S1 |  |
| REQ-STO-001 | PASS | S2 | SCN-STO-001 |
| REQ-STO-002 | PASS | S2 | SCN-STO-002 |
| REQ-STO-003 | PASS | S2 | SCN-STO-003 |
| REQ-STO-004 | PASS | S2 | SCN-STO-001 |
| REQ-STO-005 | PASS | S2 | SCN-STO-004 |
| REQ-STO-006 | PASS | S2 | SCN-STO-005 |
| SCN-STO-001 | PASS | S2 | internal/storage/conformance_integration_test.go::TestSCN_STO_001_Conformance |
| SCN-STO-002 | PASS | S2 | internal/storage/conformance_integration_test.go::TestSCN_STO_002_S3EndpointPathStylePrefix |
| SCN-STO-003 | PASS | S2 | internal/storage/azblob_gc_integration_test.go::TestSCN_STO_003_AzblobConnectionStringAndTokenCredential |
| SCN-STO-004 | PASS | S2 | internal/namespace/namespace_integration_test.go::TestSCN_STO_004_DeduplicatedFileObjects |
| SCN-STO-005 | PASS | S2 | internal/storage/azblob_gc_integration_test.go::TestSCN_STO_005_GarbageCollection |
| REQ-DOC-002 | PASS | S3 | SCN-DOC-001 |
| REQ-FLOW-001 | PASS | S3 | SCN-FLOW-001; SCN-FLOW-002 |
| REQ-FLOW-002 | PASS | S3 | SCN-FLOW-001; SCN-FLOW-002; SCN-FLOW-007 |
| REQ-FLOW-003 | PASS | S3 | SCN-FLOW-002 |
| REQ-FLOW-004 | IN_PROGRESS | S3 |  |
| REQ-FLOW-005 | IN_PROGRESS | S3 |  |
| REQ-FLOW-006 | IN_PROGRESS | S3 |  |
| REQ-FLOW-007 | IN_PROGRESS | S3 |  |
| REQ-FLOW-008 | IN_PROGRESS | S3 |  |
| REQ-NS-001 | PASS | S3 | SCN-NS-001 |
| REQ-NS-002 | IN_PROGRESS | S3 |  |
| REQ-NS-003 | IN_PROGRESS | S3 |  |
| REQ-NS-004 | IN_PROGRESS | S3 |  |
| REQ-NS-005 | IN_PROGRESS | S3 |  |
| REQ-NS-006 | IN_PROGRESS | S3 |  |
| REQ-NS-007 | IN_PROGRESS | S3 |  |
| REQ-NS-008 | IN_PROGRESS | S3 |  |
| REQ-NS-009 | PASS | S3 | SCN-NS-009 |
| REQ-UI-007 | IN_PROGRESS | S3 |  |
| SI-08 | IN_PROGRESS | S3 |  |
| SCN-FLOW-001 | PASS | S3 | internal/flow/fixtures_test.go::TestSCN_FLOW_001_ValidFixtures |
| SCN-FLOW-002 | PASS | S3 | internal/flow/fixtures_test.go::TestSCN_FLOW_002_InvalidFixtures |
| SCN-FLOW-003 | IN_PROGRESS | S3 |  |
| SCN-FLOW-004 | IN_PROGRESS | S3 |  |
| SCN-FLOW-005 | IN_PROGRESS | S3 |  |
| SCN-FLOW-006 | IN_PROGRESS | S3 |  |
| SCN-FLOW-007 | PASS | S3 | internal/flow/fixtures_test.go::TestSCN_FLOW_007_SchemaGeneratedAndApplied |
| SCN-FLOW-008 | IN_PROGRESS | S3 |  |
| SCN-NS-001 | PASS | S3 | tests/e2e/namespace_test.go::TestSCN_NS_001_NamespaceNames |
| SCN-NS-002 | IN_PROGRESS | S3 |  |
| SCN-NS-003 | IN_PROGRESS | S3 |  |
| SCN-NS-004 | IN_PROGRESS | S3 |  |
| SCN-NS-005 | IN_PROGRESS | S3 |  |
| SCN-NS-006 | IN_PROGRESS | S3 |  |
| SCN-NS-007 | IN_PROGRESS | S3 |  |
| SCN-NS-008 | IN_PROGRESS | S3 |  |
| SCN-NS-009 | PASS | S3 | tests/e2e/namespace_test.go::TestSCN_NS_009_FileAndSnapshotLimits |
| SCN-UI-010 | IN_PROGRESS | S3 |  |
| REQ-EXE-001 | OPEN | S4 | |
| REQ-EXE-002 | OPEN | S4 | |
| REQ-EXE-003 | OPEN | S4 | |
| REQ-EXE-004 | OPEN | S4 | |
| REQ-EXE-005 | OPEN | S4 | |
| REQ-EXE-006 | OPEN | S4 | |
| REQ-EXE-007 | OPEN | S4 | |
| REQ-EXE-008 | OPEN | S4 | |
| REQ-EXE-009 | OPEN | S4 | |
| REQ-EXE-010 | OPEN | S4 | |
| REQ-EXE-011 | OPEN | S4 | |
| REQ-EXE-012 | OPEN | S4 | |
| REQ-EXE-013 | OPEN | S4 | |
| REQ-EXE-014 | OPEN | S4 | |
| REQ-EXE-015 | OPEN | S4 | |
| REQ-EXE-016 | OPEN | S4 | |
| REQ-EXE-017 | OPEN | S4 | |
| REQ-EXR-001 | OPEN | S4 | |
| REQ-EXR-002 | OPEN | S4 | |
| REQ-EXR-003 | OPEN | S4 | |
| REQ-EXR-008 | OPEN | S4 | |
| REQ-RUN-001 | OPEN | S4 | |
| REQ-RUN-002 | OPEN | S4 | |
| REQ-RUN-003 | OPEN | S4 | |
| REQ-RUN-004 | OPEN | S4 | |
| REQ-RUN-005 | OPEN | S4 | |
| REQ-RUN-006 | OPEN | S4 | |
| REQ-RUN-007 | OPEN | S4 | |
| REQ-RUN-008 | OPEN | S4 | |
| REQ-RUN-009 | OPEN | S4 | |
| REQ-RUN-010 | OPEN | S4 | |
| REQ-TRG-001 | OPEN | S4 | |
| REQ-UI-004 | OPEN | S4 | |
| REQ-UI-005 | OPEN | S4 | |
| REQ-UI-012 | OPEN | S4 | |
| NFR-002 | OPEN | S4 | |
| SI-04 | OPEN | S4 | |
| SCN-EXE-001 | OPEN | S4 | |
| SCN-EXE-002 | OPEN | S4 | |
| SCN-EXE-003 | OPEN | S4 | |
| SCN-EXE-004 | OPEN | S4 | |
| SCN-EXE-005 | OPEN | S4 | |
| SCN-EXE-006 | OPEN | S4 | |
| SCN-EXE-007 | OPEN | S4 | |
| SCN-EXE-008 | OPEN | S4 | |
| SCN-EXE-009 | OPEN | S4 | |
| SCN-EXE-010 | OPEN | S4 | |
| SCN-EXE-011 | OPEN | S4 | |
| SCN-EXE-012 | OPEN | S4 | |
| SCN-EXE-013 | OPEN | S4 | |
| SCN-EXE-014 | OPEN | S4 | |
| SCN-EXE-015 | OPEN | S4 | |
| SCN-EXE-016 | OPEN | S4 | |
| SCN-EXE-017 | OPEN | S4 | |
| SCN-EXR-001 | OPEN | S4 | |
| SCN-EXR-002 | OPEN | S4 | |
| SCN-EXR-008 | OPEN | S4 | |
| SCN-NFR-002 | OPEN | S4 | |
| SCN-RUN-001 | OPEN | S4 | |
| SCN-RUN-002 | OPEN | S4 | |
| SCN-RUN-003 | OPEN | S4 | |
| SCN-RUN-004 | OPEN | S4 | |
| SCN-RUN-005 | OPEN | S4 | |
| SCN-RUN-006 | OPEN | S4 | |
| SCN-RUN-007 | OPEN | S4 | |
| SCN-RUN-008 | OPEN | S4 | |
| SCN-RUN-009 | OPEN | S4 | |
| SCN-RUN-010 | OPEN | S4 | |
| SCN-TRG-001 | OPEN | S4 | |
| SCN-UI-002 | OPEN | S4 | |
| SCN-UI-003 | OPEN | S4 | |
| SCN-UI-008 | OPEN | S4 | |
| REQ-DEP-004 | OPEN | S5 | |
| REQ-TRG-002 | OPEN | S5 | |
| REQ-TRG-003 | OPEN | S5 | |
| REQ-TRG-004 | OPEN | S5 | |
| REQ-TRG-005 | OPEN | S5 | |
| REQ-TRG-006 | OPEN | S5 | |
| SI-05 | OPEN | S5 | |
| SCN-DEP-004 | OPEN | S5 | |
| SCN-TRG-002 | OPEN | S5 | |
| SCN-TRG-003 | OPEN | S5 | |
| SCN-TRG-004 | OPEN | S5 | |
| SCN-TRG-005 | OPEN | S5 | |
| SCN-TRG-006 | OPEN | S5 | |
| SCN-TRG-007 | OPEN | S5 | |
| REQ-SEC-001 | OPEN | S6 | |
| REQ-SEC-002 | OPEN | S6 | |
| REQ-SEC-003 | OPEN | S6 | |
| REQ-SEC-004 | OPEN | S6 | |
| REQ-SEC-005 | OPEN | S6 | |
| REQ-SEC-006 | OPEN | S6 | |
| REQ-SEC-007 | OPEN | S6 | |
| REQ-SEC-008 | OPEN | S6 | |
| REQ-SEC-009 | OPEN | S6 | |
| REQ-SEC-010 | OPEN | S6 | |
| REQ-UI-008 | OPEN | S6 | |
| SI-01 | OPEN | S6 | |
| SI-09 | OPEN | S6 | |
| SI-10 | OPEN | S6 | |
| SCN-SEC-001 | OPEN | S6 | |
| SCN-SEC-002 | OPEN | S6 | |
| SCN-SEC-003 | OPEN | S6 | |
| SCN-SEC-004 | OPEN | S6 | |
| SCN-SEC-005 | OPEN | S6 | |
| SCN-SEC-006 | OPEN | S6 | |
| SCN-SEC-007 | OPEN | S6 | |
| SCN-SEC-008 | OPEN | S6 | |
| SCN-SEC-009 | OPEN | S6 | |
| SCN-SEC-010 | OPEN | S6 | |
| SCN-SEC-011 | OPEN | S6 | |
| REQ-GIT-001 | OPEN | S7 | |
| REQ-GIT-002 | OPEN | S7 | |
| REQ-GIT-003 | OPEN | S7 | |
| REQ-GIT-004 | OPEN | S7 | |
| REQ-GIT-005 | OPEN | S7 | |
| REQ-GIT-006 | OPEN | S7 | |
| REQ-GIT-007 | OPEN | S7 | |
| SCN-GIT-001 | OPEN | S7 | |
| SCN-GIT-002 | OPEN | S7 | |
| SCN-GIT-003 | OPEN | S7 | |
| SCN-GIT-004 | OPEN | S7 | |
| SCN-GIT-005 | OPEN | S7 | |
| SCN-GIT-006 | OPEN | S7 | |
| SCN-GIT-007 | OPEN | S7 | |
| SCN-GIT-008 | OPEN | S7 | |
| REQ-DEP-001 | OPEN | S8 | |
| REQ-DEP-003 | OPEN | S8 | |
| REQ-DEP-005 | OPEN | S8 | |
| REQ-EXR-004 | OPEN | S8 | |
| REQ-EXR-007 | OPEN | S8 | |
| REQ-EXR-009 | OPEN | S8 | |
| SCN-DEP-001 | OPEN | S8 | |
| SCN-DEP-003 | OPEN | S8 | |
| SCN-DEP-005 | OPEN | S8 | |
| SCN-EXR-003 | OPEN | S8 | |
| SCN-EXR-004 | OPEN | S8 | |
| SCN-EXR-006 | OPEN | S8 | |
| REQ-DEP-002 | OPEN | S9 | |
| REQ-EXR-005 | OPEN | S9 | |
| REQ-EXR-006 | OPEN | S9 | |
| SCN-DEP-002 | OPEN | S9 | |
| SCN-EXR-005 | OPEN | S9 | |
| SCN-EXR-007 | OPEN | S9 | |
| SCN-SEC-012 | OPEN | S9 | |
| REQ-UI-003 | OPEN | S10 | |
| REQ-UI-006 | OPEN | S10 | |
| REQ-UI-009 | OPEN | S10 | |
| REQ-UI-011 | OPEN | S10 | |
| REQ-UI-013 | OPEN | S10 | |
| NFR-001 | OPEN | S10 | |
| NFR-003 | OPEN | S10 | |
| SCN-NFR-001 | OPEN | S10 | |
| SCN-NFR-003 | OPEN | S10 | |
| SCN-UI-004 | OPEN | S10 | |
| SCN-UI-006 | OPEN | S10 | |
| SCN-UI-007 | OPEN | S10 | |
| SCN-UI-009 | OPEN | S10 | |
| REQ-AI-001 | OPEN | S11 | |
| REQ-AI-002 | OPEN | S11 | |
| REQ-AI-003 | OPEN | S11 | |
| REQ-AI-004 | OPEN | S11 | |
| REQ-AI-005 | OPEN | S11 | |
| REQ-AI-006 | OPEN | S11 | |
| REQ-AI-007 | OPEN | S11 | |
| REQ-AI-008 | OPEN | S11 | |
| REQ-AI-009 | OPEN | S11 | |
| SI-07 | OPEN | S11 | |
| SCN-AI-001 | OPEN | S11 | |
| SCN-AI-002 | OPEN | S11 | |
| SCN-AI-003 | OPEN | S11 | |
| SCN-AI-004 | OPEN | S11 | |
| SCN-AI-005 | OPEN | S11 | |
| SCN-AI-006 | OPEN | S11 | |
| SCN-AI-007 | OPEN | S11 | |
| SCN-AI-008 | OPEN | S11 | |
| SCN-AI-009 | OPEN | S11 | |
| REQ-EX-001 | OPEN | S12 | |
| REQ-EX-002 | OPEN | S12 | |
| SCN-EX-001 | OPEN | S12 | |
| SCN-EX-002 | OPEN | S12 | |

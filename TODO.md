# 📌 O.L.L.I. Roadmap & TODO Ideas

## 🚀 Upcoming Ideas & Planned Features

### 1. ⚙️ Declarative Workflow Orchestration Engine (선언적 워크플로우 오케스트레이션 엔진)

- **목표**: OAW(O.L.L.I. Agent Workflow Protocol) v0.1을 선언형 JSON 워크플로우로 구현해, 등록된 도구와 기존 권한 정책 안에서 검증 가능하고 제한된 순차 실행을 제공한다.
- **보안 원칙**: 임의 shell 실행, `sh -c`, 파이프, command substitution, 임의 expression 평가, 동적 tool name 생성, workflow 내부 권한 승인 및 permission bypass를 금지한다. OAW는 Registry에 등록된 고정 도구와 기존 권한 확인만 사용한다.

#### Phase 1 — Schema and Loader (스키마 및 로더)

- [x] OAW = **O.L.L.I. Agent Workflow Protocol**의 v0.1 baseline artifacts인 `workflows/agent/OAW_PROTOCOL.md`, `workflows/agent/oaw.schema.json`, `workflows/agent/image-generate-verify.oaw.json`이 정의되어 있다.
- [ ] 엔진 측에서 `workflows/agent/oaw.schema.json`을 로드하고 `oaw_version: "0.1"`, workflow name/step ID, inputs, limits, steps, outputs, `on_failure`를 fail-closed 검증한다.
- [ ] Go용 JSON Schema Draft 2020-12 validator 구현체와 정확한 버전을 pin하고, schema validation과 runtime semantic validation을 명시적으로 분리한다. schema/meta-schema는 local bundle로 embed하며 external `$ref`의 network resolution/fetch를 disable하고 fail-closed한다.
- [ ] `workflows/agent/*.oaw.json`만 검색하고, 파일명과 workflow name을 일치시킨다. UTF-8/JSON, 파일 크기, protocol version, unknown fields를 검증한다.
- [ ] 로더가 입력 타입·제약, tool/decision/return step 구조, forward-only decision target, 참조 대상과 도달 가능한 종료를 실행 전에 검증하도록 한다.
- [ ] `{{inputs.*}}`, `{{steps.*.result.*}}`, status/attempt만 허용하는 정적 binding resolver를 정의하고 환경변수·파일 내용·산술식·셸 치환을 거부한다.
- [ ] Registry 도구의 resolved arguments도 검증한다. 현재 `FunctionParamSchema`가 partial이면 complete tool-schema representation을 제공하거나 명시적인 validator adapter를 두어 unknown fields와 타입을 fail-closed 검증한다.

#### Phase 2 — Workspace and Registry Boundaries (워크스페이스 및 Registry 경계)

- [ ] workflow, schema, 실행 산출물을 immutable workspace root 내부의 고정 경로로 제한하고, `workflows/agent/OAW_PROTOCOL.md`와 `workflows/agent/oaw.schema.json`을 포함한 경로의 symlink를 거부한다.
- [ ] 별도 strict Draft 2020-12 workflow event schema와 JSONL writer를 구현한다. event별 required fields, allowed status/outcome values, `additionalProperties: false`, monotonically ordered sequence, exactly one terminal event, no events after terminal, `tool_completed` outcome categories(`succeeded|failed|cancelled|timed_out`), deterministic failed/cancelled/timed_out/skipped representation을 강제한다. `sessions/workflows/<run_id>.jsonl`은 `oaw_<32 lowercase hex>` run ID(crypto/rand 128 bits)로 descriptor-relative atomic `O_CREAT|O_EXCL|O_NOFOLLOW` 생성하며 race-safe parent traversal을 사용한다. collision/existing path는 fail-closed하고 append/merge하지 않는다.
- [ ] numeric caps를 강제한다: workflow 1 MiB, supplied input JSON 1 MiB, arguments/call 1 MiB, retained result 8 MiB, event 64 KiB, run log 8 MiB.
- [ ] raw inputs/arguments, base64/media, unrestricted results는 JSONL에 저장하지 않고 metadata, hash, bounded summaries만 보존한다.
- [ ] tool step은 시작 시 Registry-only 고정 이름으로 해석하고, 등록되지 않은 도구·동적 tool name·OAW 파일의 whitelist 추가를 거부한다.
- [ ] context-aware Registry execution contract로 migration한다. handler signature는 `func(context.Context, map[string]interface{}) (string,error)`와 동등하게 하고 central `ExecuteContext`를 사용한다. caller cancellation→cancelled/no retry, workflow deadline→timed_out/no retry, handler-local timeout만 timeout retry eligible이며 underlying work가 동일 context를 관찰하게 한다. workflow tool path에서 `context.Background`를 사용하지 않는다.
- [ ] 기존 `ask`, `accept-edit`, `auto` permissions, 사용자 승인, 민감 도구 분류를 그대로 적용하며 workflow가 이를 대신하거나 우회하지 못하게 한다. workflow-safe authorizer는 central executor에 attempt-scoped `allow`/`deny`만 반환하고 `always`를 전달·해석·persist하지 않는다. `[a] Always` 선택과 whitelist persistence는 workflow executor 밖 trusted interactive permission layer에서 현재 attempt allow 전에만 수행하며 workflow content/generic callback은 persistence를 유발할 수 없다. callback/authorizer가 없으면 필요한 permission을 fail-closed한다.
- [ ] central workflow executor가 active context와 permission callback을 받고, CLI와 agent가 같은 executor boundary를 사용하도록 한다. workflow tools는 이름별 정확히 한 번만 등록하고 Registry는 duplicate definitions를 거부하며, request-scoped state를 construction closure에 캡처하거나 request마다 재등록하지 않는다.

#### Phase 3 — Restricted Bindings and Sequential Runner (제한된 바인딩 및 순차 실행기)

- [ ] literal arguments와 치환된 arguments를 각 Registry 도구의 JSON schema로 검증하고, optional input 누락 규칙과 원래 JSON 타입 보존을 구현한다.
- [ ] `tool`, 제한된 단일 참조/operator 기반 `decision`, 최종 `return` step을 지원하는 순차 runner를 구현한다. implicit parallelism과 backward jump는 지원하지 않는다.
- [ ] 결과를 구조화된 JSON object 또는 `{ "text": "..." }`로 보관하고, 외부 콘텐츠·tool 결과의 지시문을 제어 흐름으로 해석하지 않는다.
- [ ] top-level `outputs`를 authoritative result로 사용하고, `return` step은 termination marker로만 취급한다. decision의 `stop`은 failed 상태와 no outputs로 종료한다.
- [ ] decision은 `eq`, `neq`, `gt`, `gte`, `lt`, `lte`, `contains`, `exists`, `in`만 허용하고, decision/return 결과와 skipped step을 명확히 기록한다.

#### Phase 4 — Limits, Cancellation, Timeout, and Retry (실행 상한 및 실패 제어)

- [ ] `max_steps`, `max_tool_calls`, `max_attempts_per_step`, `timeout_seconds`를 schema와 runtime에서 모두 제한하고 상한 도달 시 fail-closed로 중단한다.
- [ ] 사용자 cancel을 현재 tool context에 전달하고 이후 step을 실행하지 않으며, `failed`, `cancelled`, `timed_out`를 성공으로 변환하지 않는다.
- [ ] `tool_error`, `timeout`에 한해 bounded retry를 제공하고 protocol/step limit 중 더 작은 횟수를 적용한다. retry 중 LLM argument 자동 수정은 금지한다.
- [ ] Registry metadata/declaration contract에 `retry_safe`를 정의하고, 이를 retry의 sole source of truth로 사용한다. metadata 부재는 false이며 OAW file은 이를 설정할 수 없다. `retry_safe`인 tool만 retry할 수 있게 하고 각 retry attempt는 별도로 permission authorization을 받는다. declaration/metadata parsing, absent-default, source-of-truth tests를 추가한다.
- [ ] tool outcome/error category를 typed outcome으로 정의해 `tool_error`와 `timeout`을 구분하고, active context cancellation과 deadline을 결과에 보존한다.
- [ ] 모든 attempt 전에 permission callback을 평가하며, callback 누락은 fail-closed로 거부한다.

#### Phase 5 — JSONL Observability (JSONL 실행 기록)

- [ ] 각 실행을 `sessions/workflows/<run_id>.jsonl`에 기록하고 `workflow_started`, `step_started`, `permission_requested`, `tool_completed`, `step_retried`, `step_failed`, `workflow_completed`, `workflow_cancelled` 이벤트를 지원한다.
- [ ] 모든 이벤트에 `event_schema_version: "0.1"`, `event`, `timestamp`, `sequence`, `run_id`, `workflow`, `status`를 포함한다. `step_id`와 `attempt`는 step/permission/tool/retry events에만 required하고 workflow-level events에는 absent여야 한다. canonical planned artifact는 `workflows/agent/oaw-event.schema.json`, `$id`는 `https://olli.local/schemas/oaw-event-v0.1.schema.json`이며 event-specific strict branches는 `additionalProperties: false`로 이를 강제한다. 이 artifact는 engine plan으로 현재 생성하지 않는다.
- [ ] 정상 종료, 실패, cancel, timeout 및 실행되지 않은 step의 `skipped` 상태를 재현 가능하게 검증한다. `workflow_completed` terminal status는 succeeded|failed|timed_out, `workflow_cancelled`는 cancelled, `step_failed`는 non-terminal이다. sequence monotonicity, exactly-one terminal, no post-terminal events를 강제하고 `on_failure: report`는 bounded failure summary와 failed terminal을 기록한다.
- [ ] 실행 중에는 hidden staging `sessions/workflows/.<run_id>.jsonl.partial`만 사용하고 descriptor-relative `O_CREAT|O_EXCL|O_NOFOLLOW`, race-safe parent traversal로 생성한다. 각 event append 전 offset을 기록하고 write error/short write 시 writer lock 아래 truncate+seek rollback 후 실행을 중단한다. terminal write와 staging fsync 성공 후 descriptor-relatively no-replace promotion으로 finalized `sessions/workflows/<run_id>.jsonl`을 만들고 parent directory를 fsync한다. collision/overwrite/append/merge는 거부한다. readers/replay는 finalized non-hidden logs만 수용하고 `.partial`/`.invalid`는 무시한다. write/rollback/fsync/promotion/dir fsync 실패는 out-of-band `log_unavailable`이며 rollback 실패 시 symlink를 따르지 않는 best-effort `.invalid` quarantine을 수행한다. exactly-one-terminal/no-post-terminal은 finalized logs에만 적용한다. 각 event는 완전 serialize/size-validate 후 lock 아래 one complete line으로 append한다. 8 MiB run-log 중 64 KiB를 exactly one terminal event에 reserve하며 non-terminal은 reserve를 소비하지 않는다. reserve boundary 초과 시 event를 쓰지 않고 `log_limit` failed terminal을 reserve에 기록한다(실제 user cancellation은 cancelled terminal). 64 KiB event cap과 64 KiB terminal reserve는 complete JSONL line(UTF-8 serialized JSON bytes + 정확히 하나의 trailing `\n` delimiter)을 측정한다. complete line maximum은 65,536 bytes이며 JSON payload alone은 최대 65,535 bytes다. 8 MiB run-log cap도 모든 complete-line delimiter를 포함하고, size validation은 serialization과 delimiter addition 후 write 전에 수행한다. terminal event도 complete line 기준 64 KiB 이하여야 한다. 65,535-byte payload + newline은 accept, 65,536-byte payload + newline은 reject, exact terminal reserve boundary를 테스트한다.
- [ ] JSONL event schema/writer의 numeric caps와 metadata/hash/bounded-summary 정책을 검증하고, raw inputs/arguments/base64/media/unrestricted results를 거부한다.

#### Phase 6 — CLI and Main-Agent Surface (CLI 및 메인 에이전트 연동)

- [ ] CLI spelling을 `/workflow list|show|validate|run`으로 고정하고, `main.go`의 completion과 help를 함께 갱신한다. 실행 전 validation 실패 시 어떤 step도 실행하지 않는다.
- [ ] 메인 에이전트에 `list_workflows`, `get_workflow`, `run_workflow`를 추가하고, 조회·검증·실행 결과와 실패를 구조화해 반환한다.
- [ ] workflow tools를 main-agent에 request마다 등록하지 않고 영구적으로 한 번만 등록하며, `run_workflow`가 active context와 permission callback을 받게 한다.
- [ ] CLI와 main-agent가 central workflow executor의 동일한 active-context/permission boundary를 사용하도록 한다.
- [ ] workflow 이름과 경로를 고정된 `workflows/agent/*.oaw.json` 범위에서만 해석하고, 원격 다운로드나 즉시 실행을 허용하지 않는다.

#### Phase 7 — Exhaustive Safe Tests (포괄적 안전성 테스트)

- [ ] schema/loader, filename matching, unknown fields, static refs, missing/future/cyclic refs, type validation, decision DAG, return reachability를 테스트한다. Valid top-level outputs authority, values 없는 return marker, structural rejection of `return.values`, top-level outputs만 success result가 되는지, `on_failure: report`의 structured failure와 non-success terminal status를 테스트한다. Event schema path/$id/version/discriminator, complete event/status matrix, reserve boundary, exact-limit/overflow terminal recording, terminal 64KiB fit, atomic-line/short-write handling도 테스트한다.
- [ ] symlink·`..`·workspace escape, arbitrary shell/expression eval, dynamic tool names, Registry 누락, permission bypass, secret interpolation 및 malicious result instruction을 모두 거부하는 테스트를 추가한다.
- [ ] limits, retry, cancel, timeout, skipped steps, JSONL redaction/size bounds, CLI 및 main-agent surfaces를 성공·실패 경로 양쪽에서 exhaustively 검증한다.
- [ ] every-attempt permission approval/denial, missing callback, explicit-user-only `always` isolation, cancellation during an active handler, workflow deadline versus handler-local timeout mapping, deadline propagation through network/process handlers, duplicate side effects, and output suppression on decision `stop`을 검증한다.
- [ ] run ID format/128-bit entropy, existing-log refusal, collision refusal, symlink-swap writer races와 descriptor-relative `O_NOFOLLOW` parent traversal, schema/semantic boundary, tool-argument unknown fields, duplicate registration, request-scoped state isolation, retry-safe metadata default-false/source-of-truth, typed `tool_error`/`timeout` outcomes를 검증한다. Short non-terminal/terminal writes, rollback success/failure, process crash partials, file/directory fsync failures, promotion collision/no-replace, reader ignoring partial/invalid, and acceptance of only complete finalized logs도 검증한다.
- [ ] strict event terminal invariants(sequence monotonicity, exactly one terminal, no post-terminal events), deterministic outcome/skipped encoding, validator network isolation and embedded local meta/schema bundle를 검증한다.

#### Later Protocol Versions (향후 프로토콜)

- [ ] 구조화된 inspect criteria(`passed | failed | uncertain`)와 명시적 bounded refinement를 후속 protocol에서 설계한다.
- [ ] 안전성·비용·횟수 상한이 검증된 뒤에만 bounded refinement를 도입하며, v0.1에서는 LLM self-modification과 자동 재생성을 제공하지 않는다.
- [ ] parallel DAG 실행은 별도 protocol version에서 의존성·자원·실패 전파를 정의한 뒤 추가한다. v0.1은 순차 실행만 지원한다.
- [ ] v0.1 제외 기능을 후속 protocol에서만 검토한다: parallel step, backward jump와 무제한 loop, arbitrary code 또는 expression 실행, dynamic tool names, workflow 내부 permission grants, LLM self-modification, remote workflow download 및 즉시 실행, secrets/credential interpolation, hidden automatic regeneration.
- [ ] 위 제외 항목은 v0.1에서 명시적으로 금지하며, 후속 버전에서도 bounded refinement와 structured inspect criteria의 안전성·비용·권한 경계를 먼저 정의한다.

---

## 🎯 Completed Features (구현 완료 내역)

- [x] **O.L.L.I. 6대 서브에이전트 드림팀 구축**
  - 🔍 `delegate_researcher`: 웹 정보 조사
  - 💻 `delegate_coder`: 소프트웨어 코드 작성 및 파일 수정
  - 🧪 `delegate_tester`: `go test` 및 빌드 터미널 동적 실측
  - 🧐 `delegate_reviewer`: 코드 품질, 가독성, 엣지 케이스 정적 리뷰
  - 📝 `delegate_documenter`: 마크다운 기술 문서, README 작성
  - 📊 `delegate_presenter`: 글래스모피즘 인터랙티브 HTML PPT 슬라이드 생성
- [x] **단일 색상 톤 + 밝기/스타일 구분 체계 (Single-Hue Multi-Intensity Design)**
  - 메인 에이전트 및 서브에이전트별 시그니처 1색상 톤(Cyan, Amber, Magenta, Emerald, Blue, White) + 밝기(Bold)/스타일(Italic/Dim) 구분.
- [x] **동적 화이트리스트 설정 (`config.json`) 및 승인 옵션**
  - `accept-edit` 모드 지원 및 대화형 권한 창에서 `[a] Always` 화이트리스트 등록 지원.
- [x] **철통 보안 방어막 (`tools/security.go`)**
  - 워크스페이스 경계 탈출(`..`), 홈/루트 디렉토리 파괴(`rm -rf ~`, `rm -rf /`), 자기 삭제(`rm -rf .`) 무조건 차단.
- [x] **Ollama 10분 스트리밍 타임아웃 방어막**
  - 대용량 서브에이전트 코드 출력 시 `context deadline exceeded` 튕김 방지.

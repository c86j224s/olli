# O.L.L.I. Agent Workflow Protocol (OAW) v0.1

## 1. 목적

OAW(O.L.L.I. Agent Workflow Protocol)는 O.L.L.I.가 반복적인 다단계 작업을 예측 가능하고 안전하게 수행하기 위한 선언형 프로토콜이다. OAW 문서는 **실행 가능한 도구 절차**를 기술하며, ComfyUI 내부 그래프를 기술하는 `workflows/comfyui/*.json`과는 별개다.

OAW v0.1의 목표는 다음과 같다.

- 작은 로컬 모델이 도구 순서와 결과 전달을 매번 추론하지 않도록 한다.
- 등록된 O.L.L.I. 도구만 실행하고 기존 권한 정책을 그대로 적용한다.
- 입력, 단계, 결과 바인딩, 분기, 재시도, 최종 출력을 기계적으로 검증한다.
- 실패를 조용히 무시하지 않고 명시적으로 중단하거나 보고한다.
- 실행량을 상한으로 제한해 무한 루프와 과도한 자원 사용을 막는다.

## 2. 파일과 식별자

- 표준 확장자: `*.oaw.json`
- 기본 위치: `workflows/agent/`
- JSON Schema: `workflows/agent/oaw.schema.json`
- 프로토콜 버전: `oaw_version: "0.1"`
- workflow 이름과 step ID는 `^[a-z][a-z0-9_-]{0,63}$`을 따른다.
- 이름은 파일명과 일치해야 한다. 예: `name: "image-generate-verify"` → `image-generate-verify.oaw.json`
- `run_id`는 crypto/rand로 생성한 128비트 값을 lowercase hex로 인코딩한 `oaw_<32 lowercase hex>` 형식이어야 한다.

OAW 파일은 데이터이며 신뢰된 명령문이 아니다. 엔진은 스키마 검증을 통과한 선언만 해석하고, 문자열 안의 지시문이나 셸 구문을 실행해서는 안 된다.

## 3. 최상위 구조

```json
{
  "oaw_version": "0.1",
  "name": "image-generate-verify",
  "description": "Generate an image and inspect the result.",
  "inputs": {},
  "limits": {},
  "steps": [],
  "outputs": {},
  "on_failure": "stop"
}
```

### 3.1 `inputs`

workflow 실행자가 제공하는 값의 타입과 제약을 선언한다.

지원 타입:

- `string`
- `integer`
- `number`
- `boolean`

각 입력은 다음 필드를 사용할 수 있다.

- `type`: 필수
- `description`: 선택
- `required`: 기본값 `false`
- `default`: 선택. `required: true`와 동시에 사용할 수 없다.
- `enum`: 선택. 허용되는 literal 목록
- `minimum`, `maximum`: 숫자 입력에만 사용
- `min_length`, `max_length`: 문자열 입력에만 사용

엔진은 선언되지 않은 실행 입력을 거부해야 한다.

### 3.2 `limits`

모든 workflow는 실행 상한을 선언해야 한다.

- `max_steps`: 실행 가능한 총 step 수. v0.1 최대 64
- `max_tool_calls`: 전체 도구 호출 수. v0.1 최대 64
- `max_attempts_per_step`: step 하나의 최대 시도 횟수. v0.1 최대 3
- `timeout_seconds`: workflow 전체 시간 제한. v0.1 최대 3600

추가 전역 상한:

- workflow 파일: 1MiB 이하
- 제공된 입력 JSON 합계: 1MiB 이하
- 호출별 resolved arguments: 1MiB 이하
- run state에 보관하는 도구 결과: 8MiB 이하
- JSONL 로그 이벤트: 64KiB 이하
- run log: 8MiB 이하

상한을 초과하면 엔진은 크게 실패시키고 실행을 중단한다. 엔진은 schema 최대값보다 큰 값을 거부하고, 런타임에서 상한 도달 시 fail-closed로 중단한다.

### 3.3 `steps`

step은 배열 순서대로 실행한다. v0.1은 암시적 병렬 실행을 지원하지 않는다.

지원 `kind`:

- `tool`: 등록된 O.L.L.I. 도구 호출
- `decision`: 이전 결과에 대한 제한된 조건 평가
- `return`: 최종 결과 반환

각 step은 고유한 `id`를 가져야 한다.

### 3.4 `outputs`

성공한 workflow가 반환할 명명된 값을 정의하는 단일 권위적 최종 출력 계약이다. 모든 값은 입력이나 완료된 step 결과를 참조해야 한다. `return` step이 실행되면 엔진은 이 최상위 `outputs`를 resolve하고 성공으로 종료한다. `stop`으로 분기하면 terminal status `failed`로 종료하며 출력을 emit하지 않는다.

### 3.5 `on_failure`

v0.1에서 허용되는 값:

- `stop`: 첫 처리 불가능한 실패에서 즉시 중단
- `report`: 실패 정보를 구조화해 반환하고 성공으로 위장하지 않음

## 4. 값 바인딩

OAW는 JSON 문자열 안에서 다음 참조만 허용한다.

```text
{{inputs.<name>}}
{{steps.<step_id>.result.<field>}}
{{steps.<step_id>.status}}
{{steps.<step_id>.attempt}}
```

예:

```json
{
  "path": "{{steps.generate.result.workspace_relative_path}}"
}
```

규칙:

1. 전체 문자열이 참조 하나인 경우 원래 JSON 타입을 유지한다.
2. 문자열 내부에 참조가 포함되면 문자열로 치환한다.
3. 존재하지 않는 참조, 미래 step 참조, 순환 참조는 검증 오류다.
4. 전체 object property 값이 공급되지 않았고 기본값도 없는 optional input 참조 하나인 경우 해당 property를 생략한다. 그 밖의 위치에서 같은 누락 optional input 참조가 발생하면 검증 또는 runtime 오류로 처리한다.
5. 환경변수, 파일 내용, 임의 함수, 산술식, 셸 치환은 지원하지 않는다.
6. 비밀값을 workflow 파일에 저장해서는 안 된다.
7. 입력 정의의 `min_length`는 `max_length` 이하이고 `minimum`은 `maximum` 이하이어야 한다. `default`와 `enum`이 함께 선언되면 `default`는 `enum`의 항목 중 하나여야 하며, `enum` 항목은 중복될 수 없다. 제공된 입력값과 default 값은 선언된 type, enum, bounds를 만족해야 한다. 이 모든 교차 필드 및 값 검증은 runtime semantic validation이 담당한다.

## 5. `tool` step

```json
{
  "id": "generate",
  "kind": "tool",
  "tool": "image_generate",
  "arguments": {
    "backend_alias": "comfyui",
    "workflow_alias": "default_workflow",
    "prompt": "{{inputs.prompt}}"
  },
  "retry": {
    "max_attempts": 1,
    "when": ["tool_error", "timeout"]
  }
}
```

실행 규칙:

- `tool`은 시작 시 Registry에 등록된 고정 이름이어야 한다. 각 workflow tool은 이름별로 정확히 한 번만 등록하며 Registry는 중복 정의를 거부한다. request-scoped context/permission state는 실행 시 주입하고 construction-time closure에 캡처하거나 request마다 재등록하지 않는다.
- 치환된 `arguments`는 해당 도구의 JSON schema로 다시 검증한다.
- 모든 tool attempt 전에 workflow-safe authorizer를 통해 attempt-scoped `allow` 또는 `deny` 결과만 받는다. central OAW executor는 `always`를 받거나 해석하거나 persist하지 않는다. 명시적 `[a] Always` 선택과 whitelist persistence는 workflow executor 외부의 trusted interactive permission layer가 현재 attempt를 allow하기 전에만 수행한다. workflow content와 generic callback은 persistence를 유발할 수 없다.
- 한 attempt의 승인으로 retry를 승인하지 않는다. 민감 도구는 각 attempt별 별도 승인이어야 하며, missing authorizer가 permission을 요구하는 경우 fail-closed한다.
- OAW 파일은 도구를 whitelist에 추가하거나 사용자 승인을 대신할 수 없다.
- 도구 결과가 JSON 문자열이면 `result`는 JSON object로 파싱한다.
- JSON이 아닌 결과는 `{ "text": "..." }`로 보관한다.
- context-aware handler signature는 `func(context.Context, map[string]interface{}) (string, error)`와 동등해야 하며 central `ExecuteContext`를 통해 실행한다. caller cancellation은 `cancelled`/no retry, workflow deadline은 `timed_out`/no retry로 매핑한다. handler-local timeout만 `timeout` retry 대상이며, `retry_safe`, limits, per-attempt permission을 모두 만족해야 한다. underlying work는 동일 context를 관찰해야 한다.
- 도구 오류는 `status: "failed"`와 오류 정보를 기록한다.
- Registry metadata가 `retry_safe`의 유일한 source of truth이며, metadata가 없으면 false다. OAW 파일은 `retry_safe`를 설정하거나 요청할 수 없다.

지원 retry 사유:

- `tool_error`
- `timeout`

재시도는 `limits.max_attempts_per_step`과 step의 `retry.max_attempts` 중 더 작은 값으로 제한한다. Registry metadata가 해당 도구를 `retry_safe`로 표시하지 않으면 재시도하지 않는다(누락 시 false). 모든 tool attempt 전에 permission을 다시 평가하며, 한 attempt의 승인은 retry를 승인하지 않는다. 민감 도구는 각 attempt별 별도 승인이 없으면 재시도할 수 없다. v0.1은 재시도 전에 LLM이 arguments를 자동으로 수정하는 기능을 제공하지 않는다.

## 6. `decision` step

v0.1의 decision은 임의 표현식이 아니라 단일 참조와 명시적 연산자만 사용한다.

```json
{
  "id": "quality_gate",
  "kind": "decision",
  "condition": {
    "ref": "{{steps.inspect.result.assessment.confidence}}",
    "operator": "gte",
    "value": 0.7
  },
  "on_true": "report",
  "on_false": "report_uncertain"
}
```

지원 연산자:

- `eq`, `neq`
- `gt`, `gte`, `lt`, `lte`
- `contains`
- `exists`
- `in`

`on_true`와 `on_false`는 이후 step ID 또는 `stop`이어야 한다. 뒤쪽 step으로만 이동할 수 있으며 backward jump는 금지한다. 따라서 v0.1 실행 그래프는 유한 DAG다.

비전 모델의 `confidence`는 모델 자기평가이며 ground truth가 아니다. 중요한 판단에서 confidence 하나만으로 성공을 확정해서는 안 되며, `uncertainties`와 구체적 `answer`를 최종 보고에 포함해야 한다.

## 7. `return` step

```json
{
  "id": "report",
  "kind": "return"
}
```

`return`은 종료 표식이다. 실행되면 최상위 `outputs`를 resolve하고 workflow는 성공으로 종료한다. 완료 상태는 다음 중 하나다.

- `succeeded`
- `failed`
- `cancelled`
- `timed_out`

엔진은 실행하지 않은 step을 `skipped`로 기록한다.

## 8. 보안 불변식

OAW 엔진은 다음을 반드시 강제해야 한다.

1. 임의 셸, `sh -c`, 파이프, command substitution, 동적 실행 문자열을 지원하지 않는다.
2. 등록된 도구만 실행하며 도구 이름은 런타임 치환할 수 없다.
3. OAW가 기존 도구 권한 확인, 민감 도구 분류, whitelist를 우회하지 못한다. workflow declaration/data는 `always`를 요청하거나 합성할 수 없다. interactive callback의 `always`는 명시적 사용자 UI 선택에서만 허용되며 workflow content에서 추론하거나 persist하지 않는다. permission이 필요한데 callback이 없으면 fail-closed한다.
4. workflow와 실행 로그는 immutable workspace root 내부에만 둔다.
5. workflow 파일, schema, 실행 로그 경로에서 symlink를 거부한다.
6. 입력과 step argument를 JSON schema로 fail-closed 검증한다.
7. 선언되지 않은 필드는 거부한다(`additionalProperties: false`).
8. step 수, 도구 호출 수, 재시도 수, 전체 시간을 제한한다.
9. 실패, timeout, 사용자 거부를 성공으로 변환하지 않는다.
10. 외부 콘텐츠나 도구 결과에 포함된 지시문을 OAW 제어 흐름으로 해석하지 않는다.
11. 중단 요청은 현재 도구 context에 전달하고 이후 step을 실행하지 않는다.
12. JSONL 로그에는 raw supplied inputs, raw resolved arguments, image/audio/base64 payloads, unrestricted results를 기록하지 않는다. v0.1은 event identity, status/outcome과 비밀값을 포함하지 않는 bounded constant summary만 기록하며 raw 값의 hash나 비밀값 저장 기능도 도입하지 않는다.

## 9. 검증 단계

엔진은 실행 전에 다음 순서로 검증한다.

1. 파일 크기 제한과 UTF-8/JSON 파싱
2. `oaw.schema.json` 검증
3. protocol version 지원 여부
4. 파일명과 workflow 이름 일치
5. step ID 고유성
6. tool 이름 등록 여부
7. 입력 및 literal argument 타입
8. 참조 대상 존재 여부와 미래 참조 금지
9. decision target 존재 여부와 forward-only 여부
10. 도달 가능한 `return` 또는 명시적 종료 존재 여부
11. 상한 값과 권한 불변식

검증 오류가 하나라도 있으면 어떤 step도 실행하지 않는다.

## 10. 실행 로그

권장 경로:

```text
sessions/workflows/<run_id>.jsonl
```

권장 이벤트:

- `workflow_started`
- `step_started`
- `permission_requested`
- `tool_completed`
- `step_retried`
- `step_failed`
- `workflow_completed`
- `workflow_cancelled`

모든 이벤트는 `event_schema_version: "0.1"`, `event` discriminator, `timestamp`, `sequence`, `run_id`, `workflow`, `status`를 포함한다. `step_id`와 `attempt`는 step/permission/tool/retry 이벤트에만 필수이며 `workflow_started`와 workflow terminal event에는 반드시 없어야 한다. canonical strict Draft 2020-12 schema artifact는 `workflows/agent/oaw-event.schema.json`이고 `$id`는 `https://olli.local/schemas/oaw-event-v0.1.schema.json`이다. event-specific branches는 required fields, allowed status/outcome, `additionalProperties: false`를 강제한다. 이벤트는 monotonically ordered sequence를 가지며 정확히 하나의 terminal event만 허용하고 terminal 이후 이벤트를 허용하지 않는다. `workflow_completed`는 terminal이며 status는 `succeeded | failed | timed_out` 중 하나다. `workflow_cancelled`는 terminal이며 status는 반드시 `cancelled`다. `step_failed`는 terminal이 아니다. `tool_completed` status는 `succeeded | failed | cancelled | timed_out`이고 typed `outcome_category`는 각각 `success | tool_error | cancelled | timeout` 조합만 허용한다. 실행되지 않은 step은 `skipped`로 결정적으로 기록한다. `on_failure: report`는 bounded structured failure summary와 함께 `workflow_completed(status: "failed")`로 끝나며 succeeded가 아니다. 결과 전체 대신 크기가 제한된 구조화 요약을 기록한다. Engine은 이 schema를 초기화 시 local-only로 선컴파일하고 모든 event append 전에 검증한다. 기존 session 경로와 동일한 containment/security invariants를 재사용하되, workflow event schema와 descriptor-relative atomic `O_CREAT|O_EXCL|O_NOFOLLOW` writer를 별도로 구현하고 race-safe parent traversal을 사용해야 한다. `run_id` 충돌 또는 기존 log 경로가 있으면 fail-closed하며 append/merge하지 않는다. 현재 path-based session writer를 순진하게 재사용해서는 안 된다. 실행 중에는 hidden staging file `sessions/workflows/.<run_id>.jsonl.partial`만 사용한다. staging은 descriptor-relative `O_CREAT|O_EXCL|O_NOFOLLOW`와 race-safe parent traversal로 생성한다. 각 fully serialized event를 append하기 전에 현재 file offset을 기록하고, write error/short write가 발생하면 writer lock 아래 pre-event offset으로 truncate+seek하여 rollback한 뒤 execution을 중단한다. 유효한 terminal event는 64KiB reserve를 사용한다. terminal write와 staging file fsync가 성공하면 staging inode에 hidden commit marker `sessions/workflows/.<run_id>.jsonl.commit`을 no-replace hardlink로 만들고 inode identity를 검증한 뒤, staging basename을 `sessions/workflows/<run_id>.jsonl`로 descriptor-relative atomic no-replace rename한다. final과 marker가 열린 staging FD와 같은 regular inode인지 검증하고 parent directory를 fsync한 뒤 재검증한다. 열린 FD를 정확한 mode `0400`으로 바꾸는 syscall이 마지막 commit operation이며, 그 뒤에는 실패 가능한 cleanup·검증·fsync를 수행하지 않는다. final 또는 marker collision은 fail-closed하며 overwrite/append/merge하지 않는다. readers/replay는 final과 marker를 모두 `O_NONBLOCK|O_NOFOLLOW`로 열고, 두 FD가 regular·mode `0400`·same inode일 때만 finalized `<run_id>.jsonl`을 수용한다. generic session reader는 workflow log subtree를 읽지 않고 strict workflow reader에 위임한다. `.partial`, `.invalid`, marker가 없거나 `0600`인 final/marker, inode mismatch는 invalid/incomplete로 거부한다. write, rollback, file fsync, marker, atomic rename, directory fsync, identity validation, mode commit 중 하나라도 실패하면 caller에 out-of-band `log_unavailable` failure를 반환하고, promotion 이후에도 final/marker를 pathname으로 삭제하지 않아 replacement inode를 건드리지 않으며 uncommitted evidence를 보존한다. rollback 실패 시 symlink를 따르지 않는 atomic no-replace `.invalid` quarantine rename을 시도하며, 어느 경우든 reader는 이를 무시한다. exactly-one-terminal/no-post-terminal은 성공적으로 finalized된 log에 적용하고 infrastructure failure는 out-of-band로 표현한다. 각 fully serialized event는 size-validate한 뒤 writer lock 아래 하나의 complete line으로 append하며 short/partial write는 rollback 후 execution을 계속하지 않는다. 8MiB run-log cap 중 64KiB는 정확히 하나의 terminal event를 위해 reserve한다. non-terminal write는 reserve를 소비하지 않으며 reserve boundary를 넘으면 해당 이벤트를 쓰지 않고 execution을 중단한 뒤 reserve 안에 `workflow_completed` with status `failed`, `outcome_category: "log_limit"`를 정확히 하나 기록한다. 단, 실제 원인이 user cancellation이면 `workflow_cancelled`/`cancelled`를 사용한다. 64 KiB event cap과 64 KiB terminal reserve는 complete JSONL line, 즉 UTF-8 serialized JSON bytes와 정확히 하나의 trailing `\n` delimiter를 합산한 크기다. complete line maximum은 65,536 bytes이며 JSON payload alone은 최대 65,535 bytes다. 8MiB run-log cap도 모든 complete-line delimiter를 포함한다. size validation은 serialization과 delimiter addition 후, write 전에 수행한다. terminal event 자체도 complete line 기준 64KiB 이하여야 한다.

## 11. v0.1 비지원 기능

다음 기능은 의도적으로 지원하지 않는다.

- 병렬 step
- backward jump와 무제한 loop
- 임의 코드 또는 표현식 실행
- runtime tool 이름 생성
- workflow 내부 권한 승인
- LLM이 스스로 workflow 구조를 변경하는 self-modification
- remote workflow 다운로드 및 즉시 실행
- secret store와 credential interpolation
- 숨겨진 자동 재생성

필요성이 검증된 뒤 별도 protocol version에서 추가한다.

## 12. 예제

`workflows/agent/image-generate-verify.oaw.json`은 다음 절차를 정의한다.

```text
image_generate
→ inspect_image
→ 결과 및 불확실성 보고
```

이미지 재생성은 비용과 무한 반복 위험 때문에 v0.1 예제에서 자동 수행하지 않는다. 향후 구조화된 시각 기준(`passed | failed | uncertain`)과 명시적인 bounded refinement step이 도입되면 최대 1회 재생성을 추가할 수 있다.

# O.L.L.I. AI Gateway 가이드

O.L.L.I. AI Gateway는 여러 머신의 Ollama를 하나의 모델 풀로 등록하고, 역할·모델·현재 부하에 따라 서브에이전트 실행을 배정합니다. 단일 머신 설정은 그대로 유지되며 `ai_gateway.enabled`가 `false`이면 기존 로컬 `http://localhost:11434` 경로를 사용합니다.

## 지원 범위

첫 버전은 다음을 지원합니다.

- 여러 Ollama 노드 등록
- 모델과 역할 기반 라우팅
- weighted least-loaded 선택
- 노드별 동시 실행 제한
- 서브에이전트 실행 단위 lease
- 주기적인 `/api/tags` health probe
- 연속 실패 기반 circuit breaker
- 노드 drain과 resume
- Reviewer 차원 병렬 실행
- Detail Planner 병렬 실행
- 결과의 결정론적 순서 병합
- 라우팅된 노드 ID 기록

Coder는 계속 one-writer 순차 실행입니다. 파일을 수정하는 Coder를 병렬화하려면 독립 worktree 또는 overlay와 결정론적 통합 절차가 먼저 필요합니다.

## 네트워크 준비

Ollama 포트를 공용 인터넷에 직접 노출하지 마세요. 권장 구성은 다음과 같습니다.

1. Tailscale 또는 WireGuard 사설망으로 머신을 연결합니다.
2. 각 Ollama 노드는 중앙 O.L.L.I. 컨트롤러 IP만 허용합니다.
3. 가능하면 HTTPS 또는 mTLS reverse proxy를 앞에 둡니다.
4. 토큰은 `config.json`에 직접 쓰지 않고 환경 변수로 전달합니다.
5. 노드 주소는 관리자가 미리 설정하며 대화 입력으로 동적으로 등록하지 않습니다.

원격 `http://` endpoint는 기본적으로 거부됩니다. 사설망에서 평문 HTTP를 의도적으로 사용할 때만 해당 노드에 `insecure_allow_http: true`를 명시하세요. `file://`, URL credential, query, fragment, 하위 path가 들어간 endpoint는 거부됩니다.

## 권장 토폴로지

처음에는 역할이 겹치지 않는 네 종류의 노드로 시작하는 편이 운영하기 쉽습니다.

```text
O.L.L.I. Controller
├── main       : 메인 대화와 일반 도구 판단
├── coder      : 긴 소스 생성, max_concurrency 1
├── review-a/b : 전문 Reviewer와 Presenter
└── fast       : Architect, Cassandra, Detail Planner, Tester
```

| 노드 | 권장 모델 | 역할 | 권장 동시 실행 수 |
|---|---|---|---:|
| `main` | 사용자가 선택할 대화 모델 | `main` | 1 |
| `coder` | `qwen3.8:27b` | `coder` | 1 |
| `review-a`, `review-b` | `gemma4:12b` | `reviewer`, `researcher`, `documenter`, `presenter` | 머신당 1~2 |
| `fast` | `gemma4:e4b` | `planner`, `architect`, `cassandra`, `detail-planner`, `tester` | 2~3 |

GPU 메모리가 모델 하나만 수용하는 머신에서는 서로 다른 대형 모델을 한 노드에 섞지 않는 편이 좋습니다. 같은 모델을 제공하는 Reviewer 노드를 두 대 이상 등록하면 네 전문 Reviewer가 사용 가능한 슬롯에 자동 분산됩니다.

## 머신 준비

### 1. 모든 머신에서 모델 확인

노드마다 설정에 적을 모델이 정확한 이름으로 설치됐는지 확인합니다.

```bash
ollama list
```

모델 이름은 Gateway 설정과 `/api/tags` 결과가 정확히 일치해야 합니다. 태그가 다른 모델은 같은 계열이어도 다른 모델로 취급됩니다.

### 2. Ollama를 사설 인터페이스에서 수신

가능하면 Tailscale 또는 WireGuard 인터페이스 주소에만 bind합니다.

```bash
OLLAMA_HOST=100.64.0.11:11434 ollama serve
```

환경상 특정 인터페이스 bind가 어렵고 `0.0.0.0`을 사용해야 한다면 OS 방화벽에서 중앙 O.L.L.I. 컨트롤러 주소만 허용해야 합니다. Ollama 포트를 인터넷 라우터나 클라우드 보안 그룹에 공개하지 마세요.

### 3. 컨트롤러에서 연결 확인

사설망 안에서 각 노드의 모델 목록을 확인합니다.

```bash
curl --fail --silent --show-error \
  http://100.64.0.11:11434/api/tags
```

HTTPS reverse proxy를 사용한다면 해당 인증서와 hostname으로 검사합니다. 이 확인이 실패하면 O.L.L.I. 설정을 활성화하기 전에 네트워크·방화벽·Ollama bind부터 고쳐야 합니다.

### 4. 인증 정보 준비

토큰은 셸이나 서비스 관리자 환경에 주입하고 `config.json`에는 환경 변수 이름만 적습니다.

```bash
export OLLI_GPU_CODER_TOKEN='replace-with-secret'
export OLLI_GPU_REVIEW_A_TOKEN='replace-with-secret'
```

프로세스 관리자에서 실행한다면 같은 환경 변수가 O.L.L.I. 프로세스에 전달되는지 확인합니다. 환경 변수가 비어 있으면 Gateway는 해당 노드를 조용히 건너뛰지 않고 시작을 거부합니다.

## 기본 설정

```json
{
  "ai_gateway": {
    "enabled": true,
    "strategy": "least-loaded",
    "health_interval_seconds": 15,
    "failure_threshold": 3,
    "cooldown_seconds": 60,
    "nodes": [
      {
        "id": "gpu-coder",
        "endpoint": "https://gpu-coder.example.internal",
        "models": ["qwen3.8:27b"],
        "roles": ["coder"],
        "max_concurrency": 1,
        "weight": 100,
        "auth_token_env": "OLLI_GPU_CODER_TOKEN"
      },
      {
        "id": "gpu-review-a",
        "endpoint": "https://gpu-review-a.example.internal",
        "models": ["gemma4:12b"],
        "roles": ["reviewer", "researcher", "documenter", "presenter"],
        "max_concurrency": 2,
        "weight": 100,
        "auth_token_env": "OLLI_GPU_REVIEW_A_TOKEN"
      },
      {
        "id": "gpu-fast",
        "endpoint": "http://100.64.0.14:11434",
        "models": ["gemma4:e4b"],
        "roles": ["architect", "cassandra", "detail-planner", "planner", "tester"],
        "max_concurrency": 3,
        "weight": 80,
        "insecure_allow_http": true
      }
    ]
  }
}
```

## 노드 필드

| 필드 | 의미 |
|---|---|
| `id` | 로그와 상태 화면에 표시할 고유 노드 ID |
| `endpoint` | Ollama 또는 보호된 reverse proxy의 루트 URL |
| `models` | 정적으로 기대하는 모델 목록; health probe 성공 후 실제 `/api/tags` 목록으로 갱신 |
| `roles` | 이 노드에서 허용할 역할 |
| `max_concurrency` | 노드에 동시에 부여할 lease 상한, 1~64 |
| `weight` | 동일 부하에서 선호할 상대 가중치, 1~1000 |
| `auth_token_env` | Bearer token을 읽을 환경 변수 이름 |
| `headers_from_env` | 추가 HTTP header 이름과 환경 변수 이름의 매핑 |
| `insecure_allow_http` | loopback이 아닌 평문 HTTP를 명시적으로 허용 |

`models` 또는 `roles`를 비우면 해당 조건을 제한하지 않습니다. 운영 환경에서는 예측 가능한 배정을 위해 둘 다 명시하는 편이 좋습니다. 메인 대화와 `/models` 목록에 노드를 사용하려면 `roles`를 비우거나 `main`을 포함해야 합니다. Reviewer 전용 노드는 `roles: ["reviewer"]`로 두면 메인 대화가 배정되지 않습니다.

## 역할 이름

Reviewer의 세부 역할은 내부적으로 다음처럼 라우팅됩니다.

```text
reviewer.requirements
reviewer.logic
reviewer.safety
reviewer.tests
```

노드 설정에서는 상위 역할인 `reviewer` 하나만 지정하면 네 차원을 모두 허용합니다. 기타 주요 역할은 다음과 같습니다.

```text
main
planner
architect
cassandra
detail-planner
coder
tester
researcher
documenter
presenter
```

## 단계별 활성화

한 번에 모든 역할을 이동하지 말고 다음 순서로 켜는 것이 좋습니다.

### 1단계: Gateway 설정 작성

처음에는 `enabled: false`로 노드 목록을 작성하고 JSON 문법을 확인합니다.

```bash
python3 -m json.tool config.json >/dev/null
```

### 2단계: 메인 노드 한 대로 시작

`main` 역할 노드 하나만 활성화해 기존 단일 머신과 같은 동작을 확인합니다.

```json
{
  "id": "main",
  "endpoint": "http://100.64.0.10:11434",
  "models": ["gemma4:12b"],
  "roles": ["main"],
  "max_concurrency": 1,
  "weight": 100,
  "insecure_allow_http": true
}
```

`ai_gateway.enabled`를 `true`로 바꾸고 O.L.L.I.를 재시작합니다. 시작 시 설정이나 health probe가 실패하면 O.L.L.I.는 로컬 노드로 조용히 fallback하지 않고 종료합니다.

### 3단계: 상태 확인

```text
/gateway status
/models
```

`/models`에는 `main` 역할 노드에서 실제 확인된 모델만 나타납니다. Reviewer 전용 노드의 모델이 메인 모델 목록에 섞이지 않는 것이 정상입니다.

### 4단계: 읽기 전용 역할 추가

Reviewer와 Detail Planner 노드를 추가한 뒤 개발팀 작업을 실행합니다. 서브에이전트 보고서의 `Route Node`에서 실제 배정을 확인할 수 있습니다.

```text
Route Node: gpu-review-a
```

Reviewer 네 차원이 여러 노드 ID에 분산되고 결과 순서는 계속 Requirements → Logic → Safety → Tests로 유지되어야 합니다.

### 5단계: Coder 노드 추가

Coder 노드는 `max_concurrency: 1`로 시작하세요. 여러 Coder 노드를 등록할 수는 있지만 하나의 개발팀 작업 안에서는 one-writer 단계가 순차 실행됩니다. 서로 독립된 사용자 세션은 각 Coder 노드로 분산될 수 있습니다.

## 라우팅 방식

Gateway는 다음 순서로 후보를 고릅니다.

1. health probe가 성공한 노드만 유지
2. drain 또는 circuit-open 노드 제외
3. 요청 모델을 제공하는 노드만 유지
4. 요청 역할을 허용하는 노드만 유지
5. `active + 1`, 동시 실행 한도, weight로 least-loaded 점수 계산
6. 사용 가능한 semaphore가 있는 가장 낮은 점수의 노드에 lease 부여

노드의 모든 슬롯이 사용 중이면 context deadline 또는 cancel이 올 때까지 짧은 간격으로 기다립니다.

한 서브에이전트의 여러 모델 턴은 같은 lease와 노드에서 실행됩니다. 도구 호출 뒤 다음 모델 턴이 다른 머신으로 이동하지 않으므로 모델 재로딩과 실행 편차를 줄입니다.

## 병렬 실행

Gateway가 활성화된 경우 다음 읽기 전용 단계가 병렬로 실행됩니다.

### 전문 Reviewer

```text
Requirements ─┐
Logic ────────┼─ 병렬 실행 → 원래 차원 순서로 병합
Safety ───────┤
Tests ────────┘
```

모든 Reviewer는 동일한 source snapshot과 review context를 받습니다. 완료 순서와 무관하게 결과는 Requirements, Logic, Safety, Tests 순서로 합쳐집니다.

### Detail Planner

승인된 아키텍처의 각 work package를 병렬로 상세 계획합니다. 결과는 모델 응답 도착 순서가 아니라 아키텍처 패키지 순서로 정렬한 뒤 flatten합니다.

Gateway가 비활성화된 단일 노드 모드에서는 기존 순차 실행을 유지합니다.

## Circuit breaker

요청이 실패하면 lease 반환 시 노드 실패 횟수가 증가합니다. `failure_threshold`에 도달하면 `cooldown_seconds` 동안 새 요청 대상에서 제외됩니다.

- 성공하면 연속 실패 횟수 초기화
- 명시적인 context cancel은 장애로 계산하지 않음
- timeout과 서버 오류는 장애로 계산
- cooldown 뒤 후보로 다시 들어올 수 있음
- health probe 실패 시 별도로 unhealthy 처리

스트리밍 중간에 실패한 요청을 다른 노드에서 자동 재생하지 않습니다. 도구 호출이나 파일 수정이 이미 발생했을 수 있으므로 상위 역할의 기존 bounded retry 정책이 evidence를 보고 재시도 여부를 판단합니다.

## CLI 운영 명령

```text
/gateway status
/gateway nodes
/gateway drain <node-id>
/gateway resume <node-id>
```

- `status`: 즉시 health probe 후 상태 출력
- `nodes`: 현재 캐시된 상태 출력
- `drain`: 진행 중인 lease는 유지하고 새 lease 배정 중단
- `resume`: 새 lease 배정 재개

상태에는 endpoint 비밀 header나 token을 표시하지 않습니다. 서브에이전트 보고서에는 `Route Node`로 노드 ID만 기록합니다.

## 역할별 모델 설정과 조합

Gateway는 `subagents`와 `development_team`의 모델 선택을 변경하지 않습니다. 역할 설정이 모델을 고르고 Gateway가 그 모델을 실행할 머신을 고릅니다.

```json
{
  "subagents": {
    "coder": { "model": "qwen3.8:27b", "thinking": false },
    "reviewer": { "model": "gemma4:12b", "thinking": false },
    "presenter": { "model": "gemma4:12b", "thinking": false }
  }
}
```

예를 들어 Reviewer 노드가 두 대이고 모두 `gemma4:12b`를 제공하면 네 전문 Reviewer가 두 대의 동시 실행 슬롯에 분산됩니다.

## 운영 절차

### 점검

작업 전후에 다음 명령으로 노드 상태와 슬롯 사용량을 확인합니다.

```text
/gateway status
```

정상 상태 예:

```text
NODE          STATUS    ACTIVE/LIMIT  LATENCY  MODELS         ROLES
gpu-coder     healthy   0/1           24ms     qwen3.8:27b    coder
gpu-review-a  healthy   1/2           18ms     gemma4:12b     reviewer,presenter
gpu-review-b  healthy   0/2           21ms     gemma4:12b     reviewer
gpu-fast      healthy   2/3           16ms     gemma4:e4b     architect,cassandra,detail-planner,tester
```

### 유지보수 전 drain

```text
/gateway drain gpu-review-a
```

상태가 `draining`으로 바뀌면 새 lease는 배정되지 않습니다. `ACTIVE/LIMIT`의 active가 0이 된 뒤 해당 머신을 재시작하거나 모델을 갱신합니다.

유지보수 후:

```text
/gateway resume gpu-review-a
/gateway status
```

### 장애 대응

1. `unhealthy`: `/api/tags` 연결, 인증, TLS, 방화벽을 확인합니다.
2. `circuit-open`: 최근 모델 요청이 연속 실패했습니다. Ollama 로그와 GPU 메모리를 확인하고 cooldown을 기다립니다.
3. `ACTIVE/LIMIT`가 계속 가득 참: 동시 실행 수를 무작정 늘리기 전에 GPU 메모리와 실제 처리량을 확인합니다.
4. 특정 역할에 후보 없음: 역할 이름과 모델 태그가 노드 allowlist에 모두 포함됐는지 확인합니다.
5. 시작 직후 종료: 빈 인증 환경 변수, unsupported strategy, 잘못된 endpoint 또는 중복 node ID를 확인합니다.

### 안전한 롤백

멀티 노드에서 문제가 생기면 진행 중인 작업을 먼저 끝내거나 취소한 뒤:

1. `config.json`의 `ai_gateway.enabled`를 `false`로 변경합니다.
2. 컨트롤러 머신의 로컬 Ollama가 `localhost:11434`에서 동작하는지 확인합니다.
3. O.L.L.I.를 재시작합니다.

Gateway가 비활성화되면 기존 단일 Ollama 경로와 순차 Reviewer/Detail Planner 동작으로 돌아갑니다. 실행 중 설정 hot reload는 지원하지 않으므로 변경 후 반드시 재시작해야 합니다.

## 문제 해결

### `no healthy AI gateway node supports role ... and model ...`

- 역할 이름이 노드 `roles`에 있는지 확인합니다.
- 역할 설정에서 선택한 모델과 노드 `models` 태그가 정확히 일치하는지 확인합니다.
- `/gateway status`에서 unhealthy, draining, circuit-open 여부를 확인합니다.
- 실제 `/api/tags` 결과에서 모델이 사라졌다면 다시 pull하거나 설정을 수정합니다.

### O.L.L.I.가 시작되지만 메인 모델이 보이지 않음

`roles`를 명시한 노드는 `main`을 포함해야 메인 대화와 `/models`에 사용됩니다.

```json
"roles": ["main"]
```

Reviewer 전용 노드의 모델이 `/models`에 나타나지 않는 것은 의도된 동작입니다.

### Remote HTTP endpoint가 거부됨

HTTPS를 사용하거나 사설망임을 확인한 뒤 해당 노드에만 다음을 명시합니다.

```json
"insecure_allow_http": true
```

이 옵션은 인증이나 암호화를 제공하지 않습니다. 공용망에서 사용하지 마세요.

### 인증 header가 전달되지 않음

- 환경 변수 이름은 대문자·숫자·밑줄 형식이어야 합니다.
- O.L.L.I. 프로세스가 그 환경 변수를 실제로 상속했는지 확인합니다.
- redirect 대상에는 인증 정보를 전달하지 않도록 redirect 자체가 차단됩니다. endpoint를 최종 URL로 설정하세요.

### 병렬 실행인데 빨라지지 않음

- 같은 물리 GPU를 여러 endpoint로 등록해도 실제 추론은 직렬화될 수 있습니다.
- 노드별 `max_concurrency`가 1이고 노드가 한 대뿐이면 네 Reviewer는 슬롯을 기다립니다.
- 모델이 각 요청마다 unload/reload되는지 Ollama 로그를 확인합니다.
- Detail Planner 또는 Reviewer가 아닌 Coder 구간은 의도적으로 순차입니다.

## 운영 체크리스트

- [ ] 모든 노드는 사설망 또는 인증된 HTTPS로만 접근 가능
- [ ] 공용 인터넷에서 Ollama 포트가 닫혀 있음
- [ ] token은 환경 변수에 있고 config·세션 로그에 없음
- [ ] 각 노드에 `models`, `roles`, `max_concurrency`가 명시됨
- [ ] 메인 대화 노드에 `main` 역할이 있음
- [ ] Coder 노드는 처음에 `max_concurrency: 1`
- [ ] `/gateway status`에서 모든 예정 노드가 healthy
- [ ] `/models`에 메인 역할 모델만 보임
- [ ] 개발팀 보고서에 예상 `Route Node`가 기록됨
- [ ] 유지보수 전에 drain하고 active가 0인지 확인
- [ ] 장애 시 `enabled: false` 롤백 경로를 확인함

## 검증

Gateway 테스트는 실제 포트를 열지 않는 가짜 HTTP transport를 사용합니다. 따라서 외부 네트워크나 실제 Ollama 없이 다음을 검증합니다.

- 모델·역할 라우팅
- least-loaded 분배
- 동시 실행 한도와 context 취소
- circuit breaker
- drain/resume
- unsafe endpoint 거부
- concurrent lease 누수 방지
- Reviewer fan-out과 결정론적 결과 순서

```bash
scripts/safe-test ./gateway ./subagent ./config ./agent ./cli ./ollama
scripts/safe-exec __GO__ vet ./...
git diff --check
```

실제 여러 머신 통합 검증은 자격 증명과 사설 네트워크가 격리된 환경에서 수행해야 합니다. 테스트를 위해 공용 Ollama endpoint를 만들지 마세요.

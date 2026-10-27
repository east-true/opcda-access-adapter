# MCP stdio API

MCP는 기존 DA 기능을 AI 클라이언트에 제공하는 선택적 프런트엔드다.
**기본값은 HTTP이며 `mcp`를 명시적으로 선택한 경우에만 활성화된다.**
한 프로세스는 하나의 로컬 OPC DA 서버와 하나의 프런트엔드만 실행한다.
MCP를 선택하면 HTTP·gRPC·OPC UA 리스너를 실행하지 않는다.

## 실행 설정

Windows에서 `detect`로 확인하고 선택한 정확한 CLSID를 사용한다.
아래 CLSID는 자리표시자이며 실제 서버 값으로 교체해야 한다.

```json
{
  "version": 4,
  "source": {"clsid": "{AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA}"},
  "frontend": {"type": "mcp"},
  "writeEnabled": false
}
```

MCP 클라이언트의 로컬 stdio 설정 예시다. 필드 배치는 클라이언트의 설정 형식을 따른다.

```json
{
  "mcpServers": {
    "opcda": {
      "command": "C:\\opcda\\opcda-access-adapter.exe",
      "args": ["run", "--config", "C:\\opcda\\mcp.json"]
    }
  }
}
```

실행 파일의 386·amd64 아키텍처는 COM 등록과 일치해야 한다.
stdin/stdout은 MCP 클라이언트가 연결하며 stdout에는 JSON-RPC만 출력한다.
Windows Service 설치는 지원하지 않는다. `setup`의 네 번째 프런트엔드에서 MCP를 고르면
설정 저장만 제공한다. 환경변수 경로에서는 `OPCDA_FRONTEND=mcp`와 기존 단일 소스 설정을
사용한다. 설정 파일 실행은 환경변수와 합쳐지지 않는다.

## 도구

| 이름 | 입력 | 동작 |
|---|---|---|
| `da_status` | `{}` | 소스·상태·capability·Write 정책·연결 세대 |
| `da_browse` | `path`, `filter` 선택 | 한 경로 Browse, 기본 필터 `all` |
| `da_read` | `items: [{itemId}]`, `source: "device"` 선택 | 순서를 유지하는 Device Read |
| `da_write` | `items: [{itemId, dataType, valueEncoding, value}]` | 엄격한 타입의 value-only Write 한 번 |
| `da_available_item_properties` | `itemId` | 원본 속성 식별자·설명·타입 목록 |
| `da_item_properties` | `itemId`, `propertyIds` | 속성 순서·부분 오류·값 존재 여부 보존 |
| `da_subscribe` | `items`, `requestedUpdateRateMs`, `percentDeadband` 선택 | 세션 소유 DA 그룹 생성 |
| `da_subscription_next` | `subscriptionId`, `waitMs` 선택 | 현재 항목별 병합 집합 조회 |
| `da_unsubscribe` | `subscriptionId` | 그룹 해제 |

입력은 정확한 대소문자의 고정 JSON Schema로 검증한다. 등록 탐지·소스 자동 선택·설정 변경·
임의 코드/명령 실행 도구는 제공하지 않는다. 속성 2·3·4는 기존 계약대로 거부하며 값·Quality·
시각은 `da_read` 또는 구독으로 함께 받는다.

## 값과 오류

결과는 `structuredContent`와 같은 JSON의 `content` 텍스트로 제공한다.
값과 Read 항목의 필드는 [HTTP API](http-api.md)의 JSON 계약과 같다.
ItemID·실제/canonical VARTYPE·raw Quality·소스 시각·독립적인 시각 존재 여부·접근 권한·
HRESULT를 보존한다. Quality가 나쁜 값을 정상 측정값으로 간주하면 안 된다.
시각이 없으면 `timestampPresent: false`, `timestamp: null`이며 대체 시각을 만들지 않는다.

64비트 정수는 십진 문자열이다. NaN·무한대는 `float-special`, SAFEARRAY는 `array` 인코딩과
원소 타입·차원별 하한/길이·원소 목록을 사용한다. 미지원 타입은 명시적 오류다.

```json
{
  "name": "da_read",
  "arguments": {"items": [{"itemId": "Vendor.Example.Item"}]}
}
```

부분 실패는 결과 항목에 남고 전체 도구 실패로 승격하지 않는다.
요청/원본 메서드 전체 실패는 `isError: true`, `error.layer`, `error.code`, 필요한 경우
원본 메서드와 HRESULT로 표현한다. 입력·프로세스 값은 오류 메시지에 포함하지 않는다.
알 수 없는 도구는 SDK의 MCP 프로토콜 오류다.

Write는 기본 비활성화다. `writeEnabled: true`를 설정해야 원본 작업이 가능하다.
Write를 자동 재시도하면 안 된다. 취소·시간 초과는 원본에서 실행되지 않았다는 뜻이 아니다.
도구 annotation은 클라이언트 설명용 힌트이며 권한 통제가 아니다.

## Subscribe 수명주기

`requestedUpdateRateMs`는 1~3,600,000의 정수이고 `percentDeadband`는 0~100이다.
생성 결과는 `subscriptionId`, `connectionGeneration`, 원본이 수정한 `revisedUpdateRateMs`,
항목별 AddItems 결과와 `activeItemCount`를 포함한다.

next는 DA 코어의 현재 병합 집합을 한 번 읽는다. 기본 `waitMs`는 0이고 최대 30,000이다.
빈 대기 만료는 `results: []`다. 동일 핸들의 병렬 next는 `QUEUE_FULL`로 거부한다.
기존 요청 제한 시간(기본 10초)이 `waitMs`보다 우선한다.

프런트엔드 큐·기록·추가 샘플링은 없다. 느린 클라이언트는 코어의 항목별 최신 값만 읽고,
느리다는 이유로 그룹을 종료하지 않는다. 연결이 끊어지면 대기 값은 폐기되고
`SUBSCRIPTION_INVALIDATED`가 반환된다. 재연결해도 구독은 복원되지 않는다.
새 생성 호출이 필요하며 제거됐거나 다른 세션의 핸들은 `SUBSCRIPTION_NOT_FOUND`다.

명시적 해제와 stdin EOF·취소·전송 실패는 소유 DA 그룹을 정리한다. 세션 종료 후 Runtime도
종료한다. 재생과 자동 재구독은 없다.

## 한계와 검증

| 항목 | 한계 |
|---|---|
| 입력 JSON-RPC 한 줄 | 1 MiB |
| 출력 JSON-RPC 한 줄 | 4 MiB |
| 미완료 요청 | 16개 |
| JSON 깊이 | 64 |
| 문자열 JSON-RPC 요청 ID | 128 bytes |
| 작업 시간 | 기존 요청 제한 시간, 기본 10초 |
| DA 항목·경로·배열·그룹 | 기존 Runtime 한계 |

과대 결과는 배치를 줄여야 한다. 중복 키·잘못된 UTF-8/surrogate·과대 프레임·batch·중복
미완료 ID는 세션 오류다. Streamable HTTP·추가 인증 서비스·동시 프런트엔드는 제공하지 않는다.
프로세스는 실행 계정의 COM 권한을 사용한다.

Go SDK 클라이언트 테스트는 `go test ./...`에 포함한다. 독립 실행 파일 검증은
`python3 scripts/mcp/check.py --adapter <실행 파일>`이다. Linux에서는 미지원 플랫폼 경계를
검증한다. `--real-da --clsid <fixture CLSID>`는 허용된 OPC Foundation fixture 전용 Windows
검증이며 활성 Write 시나리오를 포함한다. [호환성 기록](compatibility.md)에 실행 결과가 남기
전에는 실제 DA 검증 완료를 주장하지 않는다. 결정과 의존성 검토는 [ADR-0020](adr/0020-mcp-stdio-frontend.md)을 따른다.

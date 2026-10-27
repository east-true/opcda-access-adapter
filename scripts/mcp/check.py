#!/usr/bin/env python3
"""SDK와 독립적인 stdio MCP 클라이언트로 실행 파일의 계약을 검증한다."""

import argparse
import json
import pathlib
import queue
import subprocess
import tempfile
import threading
import time


class Client:
    """유한 크기·시간으로 실제 어댑터 프로세스와 JSON-RPC를 교환한다."""

    def __init__(self, executable, config):
        """stdio 파이프와 값이 기록되지 않는 응답 읽기 스레드를 구성한다."""
        # 1. stdout은 프로토콜 파이프로만 사용하고 stderr는 메모리에 보관한다.
        self.process = subprocess.Popen(
            [str(executable), "run", "--config", str(config)],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )
        self.messages = queue.Queue(maxsize=16)
        self.sequence = 0
        self.stderr = bytearray()

        # 2. 응답과 진단을 분리해 프로세스 값이 터미널에 출력되지 않게 한다.
        self.reader = threading.Thread(target=self.read_messages, daemon=True)
        self.errors = threading.Thread(target=self.read_errors, daemon=True)
        self.reader.start()
        self.errors.start()

    def read_messages(self):
        """응답 한 줄의 크기와 버퍼에 보관하는 메시지 개수를 제한한다."""
        try:
            while True:
                line = self.process.stdout.readline((4 << 20) + 2)
                if not line:
                    return
                if len(line) > 4 << 20:
                    self.messages.put(None, timeout=1)
                    return
                self.messages.put(json.loads(line), timeout=1)
        except (ValueError, queue.Full):
            try:
                self.messages.put_nowait(None)
            except queue.Full:
                return

    def read_errors(self):
        """진단은 제한된 메모리에만 유지하며 테스트 실패에도 값은 출력하지 않는다."""
        while True:
            chunk = self.process.stderr.read(4096)
            if not chunk:
                return
            if len(self.stderr) < 65536:
                self.stderr.extend(chunk)

    def send(self, method, params, request_id=None):
        """한 줄에 한 JSON-RPC 객체를 보낸다."""
        message = {"jsonrpc": "2.0", "method": method, "params": params}
        if request_id is not None:
            message["id"] = request_id
        self.process.stdin.write(json.dumps(message).encode() + b"\n")
        self.process.stdin.flush()

    def call(self, method, params):
        """요청 ID가 일치하는 응답을 유한 시간 안에 받는다."""
        # 1. 호출마다 서로 다른 ID를 사용한다.
        self.sequence += 1
        self.send(method, params, self.sequence)

        # 2. 원문 응답을 오류 메시지에 포함하지 않는다.
        response = self.messages.get(timeout=20)
        assert response and response.get("id") == self.sequence, "MCP response identity mismatch"
        assert "error" not in response, "MCP protocol error"
        return response["result"]

    def tool(self, name, arguments, error=False):
        """도구의 구조화 결과와 호환용 JSON 텍스트가 일치하는지 확인한다."""
        result = self.call("tools/call", {"name": name, "arguments": arguments})
        assert bool(result.get("isError")) == error, "unexpected MCP tool outcome: " + name
        assert json.loads(result["content"][0]["text"]) == result["structuredContent"], "MCP result representations disagree"
        return result["structuredContent"]

    def close(self):
        """stdin EOF로 종료시키고 프로세스가 정상 종료했는지 검증한다."""
        # 1. MCP 호스트가 입력을 닫는 정상 종료 경로를 실행한다.
        self.process.stdin.close()
        try:
            code = self.process.wait(timeout=15)
        except subprocess.TimeoutExpired:
            self.process.kill()
            self.process.wait(timeout=5)
            raise AssertionError("MCP process did not exit on stdin EOF") from None

        # 2. 로그로 값이 유출되지 않았는지 확인하고 파이프를 닫는다.
        self.reader.join(timeout=2)
        self.errors.join(timeout=2)
        self.process.stdout.close()
        self.process.stderr.close()
        assert code == 0, "MCP process exited unsuccessfully"
        assert b'"value":' not in self.stderr and b"value=" not in self.stderr, "process value found in stderr"


def scenario(executable, clsid, real_da, write_enabled):
    """실행 파일의 초기화·도구·DA 경로·EOF를 실제 stdio로 검증한다."""
    # 1. 환경변수와 합쳐지지 않는 명시적 단일 소스 설정을 만든다.
    with tempfile.TemporaryDirectory(prefix="opcda-mcp-") as directory:
        config = pathlib.Path(directory) / "adapter.json"
        config.write_text(json.dumps({"version": 4, "source": {"clsid": clsid}, "frontend": {"type": "mcp"}, "writeEnabled": write_enabled}), encoding="utf-8")
        client = Client(executable, config)
        try:
            initialized = client.call("initialize", {"protocolVersion": "2025-11-25", "capabilities": {}, "clientInfo": {"name": "independent-python-probe", "version": "1"}})
            assert initialized["protocolVersion"] == "2025-11-25", "protocol negotiation changed"
            assert set(initialized["capabilities"]) == {"tools"}, "unexpected MCP capability"
            client.send("notifications/initialized", {})
            tools = client.call("tools/list", {})["tools"]
            assert len(tools) == 9, "unexpected tool count"
            state = client.tool("da_status", {})
            assert state["source"]["clsid"].lower() == clsid.lower(), "selected source changed"
            assert state["writeEnabled"] == write_enabled, "Write policy changed"
            if not write_enabled:
                denied = client.tool("da_write", {"items": [{"itemId": "Test/Float", "dataType": "VT_R4", "valueEncoding": "json", "value": 1}]}, error=True)
                assert denied["error"]["code"] == "WRITE_DISABLED", "Write was not blocked"
            if not real_da:
                assert state["runtimeState"] == "unsupported_platform", "expected Linux platform boundary"
                return

            # 2. 원본 연결을 기다린 뒤 Browse·부분 Read·속성·DA 구독을 검증한다.
            deadline = time.monotonic() + 30
            while state["runtimeState"] != "connected" and time.monotonic() < deadline:
                time.sleep(0.1)
                state = client.tool("da_status", {})
            assert state["runtimeState"] == "connected", "DA source did not connect"
            root = client.tool("da_browse", {})
            assert any(entry["name"] == "Test" for entry in root["entries"]), "fixture branch missing"
            nested = client.tool("da_browse", {"path": ["Test"]})
            assert {"Test/Int32", "Test/Float", "Test/String"}.issubset({entry["itemId"] for entry in nested["entries"]}), "fixture ItemIDs changed"
            read = client.tool("da_read", {"items": [{"itemId": "Test/Int32"}, {"itemId": "__opcda_adapter_invalid_item__"}]})["results"]
            assert read[0]["ok"] and read[0]["dataType"]["name"] == "VT_I4" and read[0]["quality"] == 192 and read[0]["timestampPresent"], "DA metadata changed"
            assert not read[1]["ok"] and read[1]["hresult"]["hex"] == "0xC0040007", "per-item error lost"
            available = client.tool("da_available_item_properties", {"itemId": "Test/Float"})
            ids = [prop["propertyId"] for prop in available["properties"] if prop["propertyId"] not in (2, 3, 4)]
            if ids:
                properties = client.tool("da_item_properties", {"itemId": "Test/Float", "propertyIds": ids})
                assert [prop["propertyId"] for prop in properties["results"]] == ids, "property order changed"
            group = client.tool("da_subscribe", {"items": [{"itemId": "Test/Int32"}], "requestedUpdateRateMs": 250})
            assert group["activeItemCount"] == 1 and group["revisedUpdateRateMs"] > 0, "DA group creation failed"
            update = client.tool("da_subscription_next", {"subscriptionId": group["subscriptionId"], "waitMs": 5000})
            assert len(update["results"]) == 1 and update["results"][0]["ok"], "initial DA callback missing"
            client.tool("da_unsubscribe", {"subscriptionId": group["subscriptionId"]})
            assert client.tool("da_status", {})["runtime"]["subscriptionCount"] == 0, "DA group was not released"

            # 3. 허용된 fixture에만 동일 값 Write를 한 번 실행한다.
            if write_enabled:
                item = client.tool("da_read", {"items": [{"itemId": "Test/Float"}]})["results"][0]
                mismatch = client.tool("da_write", {"items": [{"itemId": "Test/Float", "dataType": "VT_R8", "valueEncoding": "json", "value": item["value"]}]})
                assert mismatch["results"][0]["errorCode"] == "TYPE_MISMATCH", "strict Write type check missing"
                written = client.tool("da_write", {"items": [{"itemId": "Test/Float", "dataType": "VT_R4", "valueEncoding": item["valueEncoding"], "value": item["value"]}]})
                assert written["results"][0]["ok"], "same-value fixture Write failed"
            # 명시적으로 해제하지 않은 그룹은 아래 stdin EOF가 정리해야 한다.
            client.tool("da_subscribe", {"items": [{"itemId": "Test/Int32"}], "requestedUpdateRateMs": 250})
        finally:
            client.close()


def main():
    """명시적으로 지정한 실행 파일과 fixture에만 검증을 수행한다."""
    parser = argparse.ArgumentParser()
    parser.add_argument("--adapter", required=True, type=pathlib.Path)
    parser.add_argument("--clsid", default="{AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA}")
    parser.add_argument("--real-da", action="store_true")
    arguments = parser.parse_args()
    scenario(arguments.adapter.resolve(), arguments.clsid, arguments.real_da, False)
    if arguments.real_da:
        scenario(arguments.adapter.resolve(), arguments.clsid, True, True)
    print("MCP_STDIO_PASS tools=9 source=explicit stdout=protocol-only valuesLogged=false realDA=" + str(arguments.real_da).lower())


if __name__ == "__main__":
    main()

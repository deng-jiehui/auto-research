"""用真实 MCP 子进程记录 arXiv HTTP 证据，不调用模型或现有数据库。"""

import asyncio
import datetime as dt
import json
import os
from pathlib import Path
import sys
import tempfile
import time

import httpx


SAFE_HEADERS = {
    "retry-after", "content-type", "server", "date", "age", "via",
    "x-cache", "x-served-by", "x-timer", "cache-control", "x-request-id",
    "x-amzn-requestid", "cf-ray",
}


def instrument_http(trace_path):
    """只记录固定 arXiv 域名，不输出请求认证头或环境变量。"""
    original_send = httpx.AsyncClient.send

    async def traced_send(client, request, **kwargs):
        if request.url.host not in ("export.arxiv.org", "arxiv.org"):
            return await original_send(client, request, **kwargs)
        started = time.monotonic()
        record = {
            "timestamp_utc": dt.datetime.now(dt.timezone.utc).isoformat(),
            "method": request.method,
            "url": str(request.url),
        }
        try:
            response = await original_send(client, request, **kwargs)
            record["status"] = response.status_code
            record["headers"] = {
                key: value for key, value in response.headers.items()
                if key.lower() in SAFE_HEADERS
            }
            network_stream = response.extensions.get("network_stream")
            if network_stream:
                try:
                    record["server_address"] = network_stream.get_extra_info(
                        "server_addr"
                    )
                    ssl_object = network_stream.get_extra_info("ssl_object")
                    if ssl_object:
                        certificate = ssl_object.getpeercert()
                        record["tls_issuer"] = certificate.get("issuer")
                        record["tls_version"] = ssl_object.version()
                except Exception:
                    # 可选连接信息失败不影响原请求或状态记录。
                    pass
            if response.status_code >= 400:
                await response.aread()
                record["error_body_excerpt"] = response.text[:1000]
            return response
        except Exception as error:
            record["exception_type"] = type(error).__name__
            raise
        finally:
            record["seconds"] = round(time.monotonic() - started, 3)
            with trace_path.open("a", encoding="utf-8") as stream:
                stream.write(json.dumps(record, ensure_ascii=False) + "\n")

    httpx.AsyncClient.send = traced_send


async def run_client():
    """串行访问四个固定样例；首次错误后停止，不追打限流接口。"""
    from mcp import ClientSession, StdioServerParameters
    from mcp.client.stdio import stdio_client

    base_dir = Path(__file__).resolve().parents[1] / ".cache" / "diagnostics"
    base_dir.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix="arxiv-", dir=base_dir))
    trace_path = directory / "http-trace.jsonl"
    stderr_path = directory / "mcp-stderr.log"
    summary_path = directory / "results.json"
    parameters = StdioServerParameters(
        command=sys.executable,
        args=[
            str(Path(__file__).resolve()), "--server", str(trace_path),
            "--storage-path", str(directory / "storage"),
        ],
        # 只影响诊断子进程，关闭自动重试以保留首次失败。
        env={**os.environ, "ARXIV_MAX_RETRIES": "0"},
    )
    probes = [
        ("id", "get_abstract", {"paper_id": "1706.03762"}),
        ("original_exact_title", "search_papers", {
            "query": 'ti:"Attention Is All You Need"',
            "max_results": 1, "abstract_mode": "full",
        }),
        ("original_plain_title", "search_papers", {
            "query": "Attention Is All You Need",
            "max_results": 1, "abstract_mode": "full",
        }),
        ("research_topic", "search_papers", {
            "query": 'ti:"time series forecasting" AND cat:cs.LG',
            "max_results": 3, "abstract_mode": "full",
            "date_from": "2021-01-01", "date_to": "2026-10-23",
        }),
    ]
    records = []
    failed = False
    print(json.dumps({"diagnostic_directory": str(directory)}), flush=True)
    with stderr_path.open("w", encoding="utf-8") as stderr_stream:
        async with stdio_client(parameters, errlog=stderr_stream) as streams:
            async with ClientSession(*streams) as session:
                initialized = await session.initialize()
                tools = await session.list_tools()
                print(json.dumps({
                    "server": initialized.serverInfo.model_dump(),
                    "tool_count": len(tools.tools),
                }), flush=True)
                for index, (label, name, arguments) in enumerate(probes):
                    if index:
                        await asyncio.sleep(3.1)
                    started = time.monotonic()
                    result = await asyncio.wait_for(
                        session.call_tool(name, arguments), timeout=65
                    )
                    texts = [
                        block.text for block in result.content
                        if block.type == "text"
                    ]
                    try:
                        payload = json.loads(texts[0]) if texts else {}
                        if not isinstance(payload, dict):
                            raise ValueError("工具结果不是 JSON 对象")
                    except (ValueError, TypeError):
                        payload = {
                            "status": "error",
                            "message": texts[0][:1000] if texts else "空结果",
                        }
                    papers = payload.get("papers", [])
                    record = {
                        "probe": label, "tool": name,
                        "is_error": result.isError,
                        "status": payload.get("status"),
                        "http_status": payload.get("http_status"),
                        "message": payload.get("message"),
                        "total_results": payload.get("total_results"),
                        "returned": len(papers),
                        "first_title": (
                            papers[0].get("title") if papers
                            else payload.get("title")
                        ),
                        "seconds": round(time.monotonic() - started, 3),
                    }
                    records.append(record)
                    summary_path.write_text(
                        json.dumps(records, ensure_ascii=False, indent=2),
                        encoding="utf-8",
                    )
                    print(json.dumps(record, ensure_ascii=False), flush=True)
                    if result.isError or payload.get("status") in (
                        "rate_limited", "error"
                    ):
                        failed = True
                        break
    print(json.dumps({
        "http_trace": str(trace_path), "stderr": str(stderr_path),
        "results": str(summary_path),
    }), flush=True)
    return 1 if failed else 0


if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "--server":
        trace_target = Path(sys.argv[2])
        sys.argv = [sys.argv[0], *sys.argv[3:]]
        instrument_http(trace_target)
        from arxiv_mcp_server import main

        main()
    else:
        sys.exit(asyncio.run(run_client()))

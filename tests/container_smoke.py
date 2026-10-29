"""Exercise authentication and original-byte persistence in an isolated Compose project."""

import hashlib
import base64
import http.cookiejar
import io
import json
import os
from pathlib import Path
import secrets
import subprocess
import tarfile
import tempfile
import time
import urllib.error
import urllib.request


def main():
    # The host CLI must support the application's STRICT tables (SQLite >=3.37).
    subprocess.run(["sqlite3", ":memory:", "CREATE TABLE compatibility(n INTEGER) STRICT;"],
                   check=True, capture_output=True)
    project = "safevault-smoke-" + secrets.token_hex(4)
    compose = ["docker", "compose", "-p", project]
    environment = dict(
        os.environ,
        SAFEVAULT_PORT="0",
        SAFEVAULT_COOKIE_SECURE="false",
        SAFEVAULT_PUBLIC_ORIGIN="",
        SAFEVAULT_SESSION_TTL="8h",
        SAFEVAULT_SESSION_IDLE_TTL="30m",
        SAFEVAULT_MAX_UPLOAD_BYTES="26214400",
        SAFEVAULT_HOST_STORAGE_FILE="/nonexistent/disabled-for-isolated-test.json",
    )

    def run(*arguments, **options):
        return subprocess.run(compose + list(arguments), env=environment, check=True, **options)

    try:
        run("config", "--quiet")
        run("build")
        password = secrets.token_urlsafe(32)
        run("run", "--rm", "--no-deps", "-T", "--entrypoint", "/app/create-admin",
            "app", "-username", "smoke-admin", "-password-stdin",
            input=password + "\n", text=True)
        run("up", "-d", "--wait")
        def application_url():
            published = run("port", "app", "8080", capture_output=True, text=True).stdout.strip()
            assert published.startswith("127.0.0.1:"), "Non-loopback publication"
            return "http://" + published

        base_url = application_url()
        client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        csrf = ""

        def request(method, path, data=None, content_type=None, headers=None):
            request_headers = {"Origin": base_url, "X-CSRF-Token": csrf}
            if content_type:
                request_headers["Content-Type"] = content_type
            request_headers.update(headers or {})
            req = urllib.request.Request(base_url + path, data=data, method=method, headers=request_headers)
            try:
                with client.open(req, timeout=30) as response:
                    return response.status, response.read(), response.headers
            except urllib.error.HTTPError as response:
                with response:
                    return response.code, response.read(), response.headers

        status, homepage, _ = request("GET", "/")
        assert status == 200 and b"SafeVault" in homepage
        assert request("GET", "/api/documents")[0] == 401
        assert request("GET", "/api/search?q=Printer")[0] == 401
        assert request("GET", "/api/admin/storage")[0] == 401
        credentials = json.dumps({"username": "smoke-admin", "password": password}).encode()
        status, body, headers = request("POST", "/api/auth/login", credentials, "application/json")
        assert status == 200, "Login failed"
        assert "HttpOnly" in headers.get("Set-Cookie", "")
        csrf = json.loads(body)["csrf_token"]

        boundary = "safevault-" + secrets.token_hex(16)

        def upload(name, data, media_type="application/octet-stream", checksum=None):
            payload = (
                f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{name}"\r\n'
                f'Content-Type: {media_type}\r\n\r\n'
            ).encode() + data + f"\r\n--{boundary}--\r\n".encode()
            status, body, _ = request("POST", "/api/documents", payload,
                "multipart/form-data; boundary=" + boundary,
                {"X-Checksum-SHA256": checksum} if checksum is not None else None)
            assert status == 201, "Upload failed: " + name
            return json.loads(body)

        def wait_for_indexing(record, expected, attempts=None):
            deadline = time.monotonic() + 10
            while True:
                status, body, _ = request("GET", "/api/documents/" + record["id"] + "/indexing")
                assert status == 200, "Indexing status unavailable after restart"
                progress = json.loads(body)
                if progress["status"] == expected:
                    if attempts is not None:
                        assert progress["attempts"] == attempts, "Indexing attempt count changed"
                    return progress
                assert progress["status"] in ("pending", "processing"), "Indexing reached an unexpected state"
                assert time.monotonic() < deadline, "Indexing did not finish"
                time.sleep(0.1)

        fixture = Path("tests/fixtures/fictional-invoice.txt").read_bytes()
        checksum = hashlib.sha256(fixture).hexdigest()
        document = upload("fictional-invoice.txt", fixture, "text/plain", checksum)
        assert document["sha256"] == checksum

        pdf = Path("tests/fixtures/fictional-preview.pdf").read_bytes()
        pdf_document = upload("fictional-preview.pdf", pdf, "application/pdf")

        formats = [
            ("fictional-preview.docx", Path("tests/fixtures/fictional-preview.docx").read_bytes(), "ready"),
            ("fictional-preview.xlsx", Path("tests/fixtures/fictional-preview.xlsx").read_bytes(), "unsupported"),
            ("malformed.docx", b"PK\x03\x04broken synthetic package", "failed"),
        ]
        office_documents = [upload(name, data) for name, data, _ in formats]

        # Seed exact interrupted/pending states offline; timing a live parser
        # against `docker restart` would make recovery coverage nondeterministic.
        run("stop", "app")
        container = run("ps", "-a", "-q", "app", capture_output=True, text=True).stdout.strip()
        exit_code = subprocess.run(["docker", "inspect", "--format", "{{.State.ExitCode}}", container],
                                   check=True, capture_output=True, text=True).stdout.strip()
        assert exit_code == "0", "Server did not shut down cleanly"
        with tempfile.TemporaryDirectory(prefix="safevault-smoke-") as snapshot:
            run("cp", "app:/data/.", snapshot)
            database = Path(snapshot) / "metadata.sqlite"
            txt_id, docx_id = document["id"], office_documents[0]["id"]
            assert all(len(value) == 32 and all(c in "0123456789abcdef" for c in value)
                       for value in (txt_id, docx_id))
            sql = f"""
                PRAGMA wal_checkpoint(TRUNCATE);
                PRAGMA journal_mode=DELETE;
                DELETE FROM document_passages WHERE document_id IN ('{txt_id}','{docx_id}');
                UPDATE document_index_jobs SET status='processing',attempts=1,attempt_token='{'a' * 64}',last_error=''
                    WHERE document_id='{txt_id}';
                UPDATE document_index_jobs SET status='pending',attempts=0,attempt_token='',last_error='',available_at=0
                    WHERE document_id='{docx_id}';
            """
            subprocess.run(["sqlite3", str(database)], input=sql, text=True, check=True, capture_output=True)
            states = subprocess.run(["sqlite3", str(database),
                f"SELECT status FROM document_index_jobs WHERE document_id='{txt_id}';"
                f"SELECT status FROM document_index_jobs WHERE document_id='{docx_id}';"],
                check=True, capture_output=True, text=True).stdout.splitlines()
            assert states == ["processing", "pending"]
            archive = io.BytesIO()
            with tarfile.open(fileobj=archive, mode="w") as tar:
                info = tarfile.TarInfo("metadata.sqlite")
                info.uid = info.gid = 10001
                info.mode, info.size = 0o600, database.stat().st_size
                with database.open("rb") as source:
                    tar.addfile(info, source)
            subprocess.run(["docker", "cp", "-a", "-", container + ":/data/"],
                           input=archive.getvalue(), check=True)
        run("up", "-d", "--wait")
        # Docker may reassign an ephemeral host port on restart; cookies are host-bound.
        base_url = application_url()
        assert request("GET", "/api/auth/session")[0] == 200, "Session did not survive restart"
        indexing_path = "/api/documents/" + document["id"] + "/indexing"
        wait_for_indexing(document, "ready", attempts=2)
        for record, (name, original, expected) in zip(office_documents, formats):
            path = "/api/documents/" + record["id"]
            wait_for_indexing(record, expected, attempts=1 if expected == "ready" else None)
            status, downloaded, _ = request("GET", path + "/download")
            assert status == 200 and downloaded == original, "Office processing altered the original"
            status, body, _ = request("GET", path + "/preview" + ("?page=2" if name.endswith(".xlsx") else ""))
            if expected == "failed":
                assert status == 422, "Malformed DOCX did not fail safely"
            else:
                assert status == 200
                preview = json.loads(body)
                if name.endswith(".xlsx"):
                    assert preview["sheet_name"] == "Summary" and preview["rows"][0][0] == "Fictional XLSX summary 合同"
                else:
                    assert "DOCX-BROWSER-2026" in preview["text"]
                    status, body, _ = request("GET", "/api/search?q=DOCX-BROWSER-2026")
                    assert status == 200 and json.loads(body)["results"][0]["document"]["id"] == record["id"]
        for query in ("Printer", "DEMO-2026-001", "fictional-invoice"):
            status, body, _ = request("GET", "/api/search?q=" + query)
            assert status == 200, "Search failed after restart"
            results = json.loads(body)
            assert len(results["results"]) == 1 and not results["has_more"]
            hit = results["results"][0]
            assert hit["document"]["id"] == document["id"]
            if query == "Printer":
                assert hit["match"] == "content" and hit["section"] >= 1
                assert "Printer" in hit["snippet"]
        status, downloaded, headers = request("GET", "/api/documents/" + document["id"] + "/download")
        assert status == 200 and downloaded == fixture, "Original bytes changed after restart"
        assert headers["X-Checksum-SHA256"] == checksum
        preview_path = "/api/documents/" + document["id"] + "/preview"
        status, body, headers = request("GET", preview_path)
        assert status == 200 and json.loads(body)["text"] == fixture.decode()
        assert headers["Cache-Control"] == "no-store" and headers["X-Content-Type-Options"] == "nosniff"
        status, body, _ = request("GET", "/api/documents/" + pdf_document["id"] + "/preview?page=2")
        assert status == 200, "PDF raster preview failed in the read-only runtime image"
        preview = json.loads(body)
        assert preview["kind"] == "pdf" and preview["page"] == 2 and preview["pages"] == 2
        assert base64.b64decode(preview["image"], validate=True).startswith(b"\x89PNG\r\n\x1a\n")
        status, original_pdf, _ = request("GET", "/api/documents/" + pdf_document["id"] + "/download")
        assert status == 200 and original_pdf == pdf, "PDF preview changed original bytes"
        status, body, headers = request("GET", "/api/admin/storage")
        assert status == 200 and headers["Cache-Control"] == "no-store"
        metrics = json.loads(body)
        assert metrics["documents"]["count"] == 5
        assert metrics["documents"]["logical_bytes"] == len(fixture) + len(pdf) + sum(len(data) for _, data, _ in formats)
        assert sum(item["count"] for item in metrics["documents"]["by_type"]) == 5
        assert metrics["filesystem"]["source"] == "container-filesystem"
        capacity = metrics["filesystem"]["capacity"]
        assert capacity and capacity["total_bytes"] > 0
        assert capacity["used_bytes"] + capacity["available_bytes"] + capacity["reserved_bytes"] == capacity["total_bytes"]
        status, body, _ = request("GET", "/api/documents?limit=100")
        assert status == 200 and any(item["id"] == document["id"] for item in json.loads(body)["documents"])
        assert request("POST", "/api/auth/logout")[0] == 204
        assert request("GET", "/api/documents")[0] == 401
        assert request("GET", indexing_path)[0] == 401
        assert request("GET", preview_path)[0] == 401
        assert request("GET", "/api/admin/storage")[0] == 401
        assert request("GET", "/api/search?q=Printer")[0] == 401
        # Check Compose -> config -> middleware wiring without exposing a network proxy.
        origin = "https://vault.example.ts.net"
        environment.update(SAFEVAULT_PUBLIC_ORIGIN=origin, SAFEVAULT_COOKIE_SECURE="true")
        run("up", "-d", "--wait")
        base_url = application_url()
        assert request("GET", "/api/health", headers={"Origin": ""})[0] == 200
        assert request("GET", "/", headers={"Origin": ""})[0] == 403
        public_headers = {"Host": "vault.example.ts.net", "Origin": origin}
        status, body, _ = request("GET", "/api/health", headers=public_headers)
        assert status == 200 and json.loads(body)["mode"] == "private-https"
        assert request("GET", "/api/documents", headers=public_headers)[0] == 401
        assert request("GET", "/api/health", headers={"Host": "evil.example", "Origin": origin})[0] == 403
        print("PASS: indexing recovery, previews, original integrity, search, storage, sessions and configured HTTPS origin boundary")
    finally:
        # Only the randomly named test project is removed, including its synthetic volume.
        subprocess.run(compose + ["down", "--volumes", "--remove-orphans"], env=environment, check=False)


if __name__ == "__main__":
    main()

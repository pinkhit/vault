"""Exercise authentication and original-byte persistence in an isolated Compose project."""

import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import subprocess
import urllib.error
import urllib.request


def main():
    project = "familyvault-smoke-" + secrets.token_hex(4)
    compose = ["docker", "compose", "-p", project]
    environment = dict(
        os.environ,
        FAMILYVAULT_PORT="0",
        FAMILYVAULT_COOKIE_SECURE="false",
        FAMILYVAULT_SESSION_TTL="8h",
        FAMILYVAULT_SESSION_IDLE_TTL="30m",
        FAMILYVAULT_MAX_UPLOAD_BYTES="26214400",
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
        assert status == 200 and b"FamilyVault" in homepage
        assert request("GET", "/api/documents")[0] == 401
        credentials = json.dumps({"username": "smoke-admin", "password": password}).encode()
        status, body, headers = request("POST", "/api/auth/login", credentials, "application/json")
        assert status == 200, "Login failed"
        assert "HttpOnly" in headers.get("Set-Cookie", "")
        csrf = json.loads(body)["csrf_token"]

        fixture = Path("tests/fixtures/fictional-invoice.txt").read_bytes()
        checksum = hashlib.sha256(fixture).hexdigest()
        boundary = "familyvault-" + secrets.token_hex(16)
        multipart = (
            f"--{boundary}\r\n"
            'Content-Disposition: form-data; name="file"; filename="fictional-invoice.txt"\r\n'
            "Content-Type: text/plain\r\n\r\n"
        ).encode() + fixture + f"\r\n--{boundary}--\r\n".encode()
        status, body, _ = request("POST", "/api/documents", multipart,
                                  "multipart/form-data; boundary=" + boundary,
                                  {"X-Checksum-SHA256": checksum})
        assert status == 201, "Upload failed"
        document = json.loads(body)
        assert document["sha256"] == checksum

        run("restart", "app")
        run("up", "-d", "--wait")
        # Docker may reassign an ephemeral host port on restart; cookies are host-bound.
        base_url = application_url()
        assert request("GET", "/api/auth/session")[0] == 200, "Session did not survive restart"
        status, downloaded, headers = request("GET", "/api/documents/" + document["id"] + "/download")
        assert status == 200 and downloaded == fixture, "Original bytes changed after restart"
        assert headers["X-Checksum-SHA256"] == checksum
        status, body, _ = request("GET", "/api/documents?limit=100")
        assert status == 200 and any(item["id"] == document["id"] for item in json.loads(body)["documents"])
        assert request("POST", "/api/auth/logout")[0] == 204
        assert request("GET", "/api/documents")[0] == 401
        print("PASS: authenticated upload/download, checksums, restart persistence and logout revocation")
    finally:
        # Only the randomly named test project is removed, including its synthetic volume.
        subprocess.run(compose + ["down", "--volumes", "--remove-orphans"], env=environment, check=False)


if __name__ == "__main__":
    main()

"""Synthetic dataset, offline commands and interruption-safe generation tests."""

import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from types import SimpleNamespace
from unittest.mock import Mock, patch

import pandas as pd
import pytest


ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("qa_integrity", ROOT / "qa_dataset.py")
qa = importlib.util.module_from_spec(spec)
spec.loader.exec_module(qa)


def samples():
    return {name: pd.read_parquet(ROOT / "samples" / f"{name}.parquet")
            for name in ("queries", "corpus", "qrels", "answers", "qas")}


def write_frames(directory, frames):
    directory.mkdir(exist_ok=True)
    for name, frame in frames.items():
        frame.to_parquet(directory / f"{name}.parquet", index=False)


def fake_client():
    client = Mock()
    client.chat.completions.create.return_value = SimpleNamespace(choices=[
        SimpleNamespace(message=SimpleNamespace(content="synthetic generated answer"))
    ])
    return client


def test_shipped_synthetic_parquet_has_consistent_evaluation_relations():
    data = samples()
    for name in ("queries", "corpus", "answers"):
        assert list(data[name].columns) == ["id", "text"]
        assert str(data[name]["id"].dtype) == "int64"
        assert data[name]["id"].is_unique
        assert data[name]["text"].map(lambda value: isinstance(value, str) and bool(value.strip())).all()
    for frame, column, target in (("qrels", "qid", "queries"), ("qrels", "pid", "corpus"),
                                  ("qas", "qid", "queries"), ("qas", "aid", "answers")):
        assert str(data[frame][column].dtype) == "int64"
        assert set(data[frame][column]) <= set(data[target]["id"])
    assert set(data["qrels"]["qid"]) == set(data["queries"]["id"])
    assert set(data["qas"]["qid"]) == set(data["queries"]["id"])
    assert (data["qrels"]["score"] > 0).all()


def test_show_default_limit_works_offline_with_three_samples():
    output = io.StringIO()
    with patch.object(qa.openai, "Client", side_effect=AssertionError("must stay offline")), contextlib.redirect_stdout(output):
        qa.show_results(str(ROOT / "samples"))
    assert output.getvalue().count("Question (qid=") == 3


def test_completed_resume_does_not_require_credentials(tmp_path):
    write_frames(tmp_path, samples())
    with patch.object(qa.openai, "Client", side_effect=AssertionError("must stay offline")):
        qa.generate_answers(str(tmp_path), str(tmp_path))


@pytest.mark.parametrize("string_ids", [False, True])
def test_resume_does_not_reuse_noncontiguous_answer_ids(tmp_path, string_ids):
    data = samples()
    answer_ids = ["first", "third"] if string_ids else [1, 3]
    data["answers"] = pd.DataFrame({"id": answer_ids, "text": ["saved one", "saved two"]})
    data["qas"] = pd.DataFrame({"qid": [1, 2], "aid": answer_ids})
    write_frames(tmp_path, data)
    client = fake_client()
    with patch.object(qa.openai, "Client", return_value=client):
        qa.generate_answers(str(tmp_path), str(tmp_path), model="local-model")
    answers = pd.read_parquet(tmp_path / "answers.parquet")
    pairs = pd.read_parquet(tmp_path / "qas.parquet")
    assert answers["id"].is_unique
    assert pairs["qid"].is_unique
    assert answers["text"].tolist() == ["saved one", "saved two", "synthetic generated answer"]
    if not string_ids:
        assert answers["id"].tolist() == [1, 3, 4]
    assert client.chat.completions.create.call_count == 1


def test_dangling_resume_reference_is_regenerated(tmp_path):
    data = samples()
    data["answers"] = data["answers"].iloc[:1]
    data["qas"] = pd.DataFrame({"qid": [1], "aid": [999]})
    write_frames(tmp_path, data)
    client = fake_client()
    with patch.object(qa.openai, "Client", return_value=client):
        qa.generate_answers(str(tmp_path), str(tmp_path))
    pairs = pd.read_parquet(tmp_path / "qas.parquet")
    answers = pd.read_parquet(tmp_path / "answers.parquet")
    assert set(pairs["qid"]) == {1, 2, 3}
    assert set(pairs["aid"]) <= set(answers["id"])
    assert 999 not in set(pairs["aid"])
    assert client.chat.completions.create.call_count == 3


def test_failed_checkpoint_write_preserves_previous_file(tmp_path):
    path = tmp_path / "answers.parquet"
    data = samples()["answers"]
    data.to_parquet(path)
    before = path.read_bytes()
    with patch.object(pd.DataFrame, "to_parquet", side_effect=OSError("disk full")):
        with pytest.raises(OSError, match="disk full"):
            qa.save_to_parquet(data, str(path))
    assert path.read_bytes() == before
    assert list(tmp_path.iterdir()) == [path]


def test_invalid_checkpoint_schema_is_not_silently_overwritten(tmp_path):
    data = samples()
    data["answers"] = pd.DataFrame({"wrong": [1]})
    write_frames(tmp_path, data)
    before = (tmp_path / "answers.parquet").read_bytes()
    with pytest.raises(ValueError, match="Invalid checkpoint"):
        qa.generate_answers(str(tmp_path), str(tmp_path))
    assert (tmp_path / "answers.parquet").read_bytes() == before


@pytest.mark.parametrize("content", [None, "", "   "])
def test_empty_provider_answer_is_not_accepted(content):
    data = samples()
    client = fake_client()
    client.chat.completions.create.return_value.choices[0].message.content = content
    with patch.object(qa.openai, "Client", return_value=client):
        system = qa.QAAnsweringSystem(data["queries"], data["corpus"], data["qrels"])
        with pytest.raises(ValueError, match="empty or filtered"):
            system.answer_question(1)


def test_actual_sdk_roundtrip_uses_selected_model_and_writes_parquet(tmp_path):
    requests = []

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def do_POST(self):
            payload = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            requests.append((self.path, payload))
            body = json.dumps({"id": "local-answer", "object": "chat.completion", "created": 0,
                               "model": payload["model"], "choices": [{"index": 0, "finish_reason": "stop",
                               "message": {"role": "assistant", "content": "local SDK answer"}}]}).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

    input_dir = tmp_path / "input"
    output_dir = tmp_path / "output"
    write_frames(input_dir, {name: frame for name, frame in samples().items()
                             if name in ("queries", "corpus", "qrels")})
    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        with patch.dict(os.environ, {"OPENAI_API_KEY": "local-placeholder",
                                   "OPENAI_BASE_URL": f"http://127.0.0.1:{server.server_port}/v1"}):
            qa.generate_answers(str(input_dir), str(output_dir), model="local-model")
    finally:
        server.shutdown()
        server.server_close()
        thread.join(5)
    assert len(requests) == 3
    assert all(path == "/v1/chat/completions" and body["model"] == "local-model"
               for path, body in requests)
    assert pd.read_parquet(output_dir / "answers.parquet")["text"].tolist() == ["local SDK answer"] * 3

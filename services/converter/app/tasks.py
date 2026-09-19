"""In-process task registry for asynchronous conversions.

Conversion of a scanned paper can run for minutes. Holding an HTTP request open
that long is fragile, so callers submit a job and poll it. A thread pool bounds
how many documents convert at once, which is the real memory constraint.
"""

from __future__ import annotations

import logging
import threading
import time
import uuid
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass, field
from typing import Any

from .engine import ConvertOptions, ConvertResult, convert
from .settings import settings

log = logging.getLogger("converter.tasks")


@dataclass
class Task:
    id: str
    filename: str
    status: str = "queued"  # queued | processing | completed | failed | cancelled
    stage: str = "queued"
    progress: int = 0
    error: str = ""
    created_at: float = field(default_factory=time.time)
    started_at: float = 0.0
    finished_at: float = 0.0
    result: ConvertResult | None = None
    cancelled: bool = False

    def as_dict(self, include_result: bool = True) -> dict[str, Any]:
        payload: dict[str, Any] = {
            "task_id": self.id,
            "filename": self.filename,
            "status": self.status,
            "stage": self.stage,
            "progress": self.progress,
            "created_at": self.created_at,
            "started_at": self.started_at or None,
            "finished_at": self.finished_at or None,
        }
        if self.error:
            payload["error"] = self.error
        if include_result and self.result is not None:
            payload["result"] = self.result.as_dict()
        return payload


class TaskRegistry:
    def __init__(self, concurrency: int, ttl_seconds: int) -> None:
        self._tasks: dict[str, Task] = {}
        self._lock = threading.Lock()
        self._pool = ThreadPoolExecutor(
            max_workers=max(1, concurrency), thread_name_prefix="convert"
        )
        self._ttl = ttl_seconds

    def submit(self, data: bytes, filename: str, extension: str, mime_type: str,
               opts: ConvertOptions) -> Task:
        self._evict_expired()
        task = Task(id=uuid.uuid4().hex, filename=filename)
        with self._lock:
            self._tasks[task.id] = task
        self._pool.submit(self._run, task, data, filename, extension, mime_type, opts)
        return task

    def _run(self, task: Task, data: bytes, filename: str, extension: str,
             mime_type: str, opts: ConvertOptions) -> None:
        if task.cancelled:
            self._finish(task, "cancelled", "cancelled before it started")
            return

        with self._lock:
            task.status = "processing"
            task.stage = "starting"
            task.started_at = time.time()

        def on_stage(name: str, percent: int) -> None:
            with self._lock:
                task.stage = name
                task.progress = max(task.progress, min(99, percent))

        try:
            result = convert(data, filename, extension, mime_type, opts, on_stage=on_stage)
        except Exception as exc:  # the client needs the reason, not a stack trace
            log.exception("conversion failed for %s", filename)
            self._finish(task, "failed", f"{type(exc).__name__}: {exc}")
            return

        with self._lock:
            task.result = result
            task.status = "completed"
            task.stage = "done"
            task.progress = 100
            task.finished_at = time.time()

    def _finish(self, task: Task, status: str, error: str) -> None:
        with self._lock:
            task.status = status
            task.error = error
            task.stage = status
            task.finished_at = time.time()

    def get(self, task_id: str) -> Task | None:
        with self._lock:
            return self._tasks.get(task_id)

    def cancel(self, task_id: str) -> bool:
        """Mark a task cancelled. Work already inside the engine still finishes."""
        with self._lock:
            task = self._tasks.get(task_id)
            if task is None:
                return False
            if task.status in {"completed", "failed", "cancelled"}:
                self._tasks.pop(task_id, None)
                return True
            task.cancelled = True
            if task.status == "queued":
                task.status = "cancelled"
                task.stage = "cancelled"
                task.finished_at = time.time()
            return True

    def stats(self) -> dict[str, int]:
        with self._lock:
            queued = sum(1 for t in self._tasks.values() if t.status == "queued")
            running = sum(1 for t in self._tasks.values() if t.status == "processing")
            return {"queued": queued, "running": running, "tracked": len(self._tasks)}

    def _evict_expired(self) -> None:
        cutoff = time.time() - self._ttl
        with self._lock:
            stale = [
                task_id
                for task_id, task in self._tasks.items()
                if task.finished_at and task.finished_at < cutoff
            ]
            for task_id in stale:
                self._tasks.pop(task_id, None)


registry = TaskRegistry(settings.concurrency, settings.task_ttl_seconds)

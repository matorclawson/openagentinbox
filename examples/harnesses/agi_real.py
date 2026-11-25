"""Send a durable completion notice for one locally configured REAL benchmark run."""
import argparse
from uuid import UUID
from openagentinbox import Inbox


def report(run_id, recipient, task):
    return Inbox.from_env().send(recipient, "REAL benchmark completed", task, str(UUID(run_id)))


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("run_id", type=UUID, help="Persist this UUID with the experiment")
    parser.add_argument("recipient")
    parser.add_argument("task")
    parser.add_argument("--report-only", action="store_true", help="Retry notification for an already completed run")
    args = parser.parse_args()
    if not args.report_only:
        from agisdk import REAL
        REAL.harness(model="gpt-4o", task_name=args.task, headless=True, leaderboard=False).run()
    print(report(str(args.run_id), args.recipient, args.task)["id"])

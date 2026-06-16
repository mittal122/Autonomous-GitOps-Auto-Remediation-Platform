"""Learner service entrypoint.

TODO (future prompt): start background worker, wire up outcome store
"""

import os
import sys


def main() -> None:
    env = os.getenv("PYTHON_ENV", "development")
    print(f"AutoSRE Learner starting (env={env})")
    print("No subsystems active yet — foundation scaffold only.")
    sys.exit(0)


if __name__ == "__main__":
    main()

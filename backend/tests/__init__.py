"""Test package init: add backend/ to sys.path so `from app.* import ...`
works regardless of where the test runner is invoked from."""

import os
import sys

_HERE = os.path.dirname(os.path.abspath(__file__))
_BACKEND = os.path.dirname(_HERE)
if _BACKEND not in sys.path:
    sys.path.insert(0, _BACKEND)

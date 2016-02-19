#!/bin/bash
ps aux | grep "runendless" | grep -v grep | awk '{print $2}' | xargs -i kill {}

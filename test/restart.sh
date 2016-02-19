#!/bin/bash
# Sends the SIGHUP signal
ps aux | grep "runendless" | grep -v grep | awk '{print $2}' | xargs -i kill -1 {}

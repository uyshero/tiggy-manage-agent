#!/bin/sh
# Demo command turn for TMA.
# It reads CommandTurnInput JSON from stdin and writes agent.message payload JSON to stdout.

input=$(cat)

case "$input" in
  *'"protocol_version":"tma.command.v1"'*'"turn_id"'*)
    printf '{"protocol_version":"tma.command.v1","content":[{"type":"text","text":"Command turn received your message."}]}'
    ;;
  *)
    printf '{"protocol_version":"tma.command.v1","content":[{"type":"text","text":"Command turn received malformed input."}]}'
    ;;
esac

#!/bin/bash
set -e

PICOBOT_HOME="${PICOBOT_HOME:-/home/picobot/.picobot}"
CONFIG="${PICOBOT_HOME}/config.json"

# Auto-onboard if config doesn't exist yet
if [ ! -f "${CONFIG}" ]; then
  echo "First run detected — running onboard..."
  picobot onboard
  echo "✅ Onboard complete. Config at ${CONFIG}"
fi

# Restore workspace from GitHub repo if configured and not already cloned.
# This ensures workspace data (memory, skills, HEARTBEAT.md) survives
# container rebuilds even without a persistent volume.
WORKSPACE="${PICOBOT_HOME}/workspace"
if [ -n "${GOOBE_WORKSPACE_REPO}" ] && [ -n "${GITHUB_TOKEN}" ]; then
  if [ ! -d "${WORKSPACE}/.git" ]; then
    echo "Restoring workspace from ${GOOBE_WORKSPACE_REPO}..."
    rm -rf "${WORKSPACE}"
    git clone "https://x-access-token:${GITHUB_TOKEN}@github.com/${GOOBE_WORKSPACE_REPO}.git" "${WORKSPACE}"
    echo "✅ Workspace restored from GitHub"
  else
    echo "Workspace already linked to git, pulling latest..."
    cd "${WORKSPACE}" && git pull --ff-only || true
    cd /home/picobot
  fi
fi

# Allow overriding config values via environment variables
if [ -n "${OPENAI_API_KEY}" ]; then
  echo "Applying OPENAI_API_KEY from environment..."
  TMP=$(mktemp)
  jq --arg key "${OPENAI_API_KEY}" '.providers.openai.apiKey = $key' "${CONFIG}" > "$TMP" && mv "$TMP" "${CONFIG}"
fi

if [ -n "${OPENAI_API_BASE}" ]; then
  echo "Applying OPENAI_API_BASE from environment..."
  TMP=$(mktemp)
  jq --arg base "${OPENAI_API_BASE}" '.providers.openai.apiBase = $base' "${CONFIG}" > "$TMP" && mv "$TMP" "${CONFIG}"
fi

if [ -n "${TELEGRAM_BOT_TOKEN}" ]; then
  echo "Applying TELEGRAM_BOT_TOKEN from environment..."
  TMP=$(mktemp)
  jq --arg token "${TELEGRAM_BOT_TOKEN}" '.channels.telegram.enabled = true | .channels.telegram.token = $token' "${CONFIG}" > "$TMP" && mv "$TMP" "${CONFIG}"
fi

if [ -n "${TELEGRAM_ALLOW_FROM}" ]; then
  echo "Applying TELEGRAM_ALLOW_FROM from environment..."
  ALLOW_JSON=$(echo "${TELEGRAM_ALLOW_FROM}" | jq -R 'split(",")')
  TMP=$(mktemp)
  jq --argjson allow "${ALLOW_JSON}" '.channels.telegram.allowFrom = $allow' "${CONFIG}" > "$TMP" && mv "$TMP" "${CONFIG}"
fi

if [ -n "${DISCORD_BOT_TOKEN}" ]; then
  echo "Applying DISCORD_BOT_TOKEN from environment..."
  TMP=$(mktemp)
  jq --arg token "${DISCORD_BOT_TOKEN}" '.channels.discord.enabled = true | .channels.discord.token = $token' "${CONFIG}" > "$TMP" && mv "$TMP" "${CONFIG}"
fi

if [ -n "${DISCORD_ALLOW_FROM}" ]; then
  echo "Applying DISCORD_ALLOW_FROM from environment..."
  ALLOW_JSON=$(echo "${DISCORD_ALLOW_FROM}" | jq -R 'split(",")')
  TMP=$(mktemp)
  jq --argjson allow "${ALLOW_JSON}" '.channels.discord.allowFrom = $allow' "${CONFIG}" > "$TMP" && mv "$TMP" "${CONFIG}"
fi

if [ -n "${PICOBOT_MODEL}" ]; then
  echo "Applying PICOBOT_MODEL from environment..."
  TMP=$(mktemp)
  jq --arg model "${PICOBOT_MODEL}" '.agents.defaults.model = $model' "${CONFIG}" > "$TMP" && mv "$TMP" "${CONFIG}"
fi

if [ -n "${PICOBOT_HB_CHANNEL}" ]; then
  echo "Applying heartbeat fallback: ${PICOBOT_HB_CHANNEL}:${PICOBOT_HB_CHATID}..."
  TMP=$(mktemp)
  jq --arg ch "${PICOBOT_HB_CHANNEL}" --arg cid "${PICOBOT_HB_CHATID:-}" \
    '.agents.defaults.heartbeatFallbackChannel = $ch | .agents.defaults.heartbeatFallbackChatID = $cid' \
    "${CONFIG}" > "$TMP" && mv "$TMP" "${CONFIG}"
fi

if [ -n "${PICOBOT_HB_INTERVAL}" ]; then
  echo "Applying heartbeat interval: ${PICOBOT_HB_INTERVAL}s..."
  TMP=$(mktemp)
  jq --argjson interval "${PICOBOT_HB_INTERVAL}" '.agents.defaults.heartbeatIntervalS = $interval' \
    "${CONFIG}" > "$TMP" && mv "$TMP" "${CONFIG}"
fi

echo "Starting picobot $@..."
exec picobot "$@"

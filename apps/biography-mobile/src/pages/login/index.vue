<script setup lang="ts">
import { onMounted, ref } from "vue";
import {
  biographyDevTokenInputEnabled,
  clearBiographyAuth,
  completeBiographyOIDCCallbackFromURL,
  currentBiographyAccessToken,
  fetchBiographyAuthConfig,
  saveBiographyOIDCToken,
  startBiographyOIDCLogin,
} from "@/services/auth";

const oidcToken = ref("");
const loading = ref(false);
const errorMessage = ref("");
const devTokenInputEnabled = biographyDevTokenInputEnabled();

onMounted(async () => {
  clearBiographyAuthIfLegacyPhoneLogin();
  try {
    const user = await completeBiographyOIDCCallbackFromURL();
    if (user) {
      uni.redirectTo({ url: "/pages/interview/index" });
      return;
    }
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : "统一身份认证失败，请重新登录";
    loading.value = false;
    return;
  }
  const config = await fetchBiographyAuthConfig();
  if (!config.enabled || currentBiographyAccessToken()) {
    uni.redirectTo({ url: "/pages/interview/index" });
    return;
  }
  await startOIDCLogin();
});

async function startOIDCLogin() {
  if (loading.value) return;
  errorMessage.value = "";
  loading.value = true;
  let url = "";
  try {
    url = await startBiographyOIDCLogin();
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : "统一身份认证暂时不可用";
    loading.value = false;
    return;
  }

  // #ifdef H5
  window.location.href = url;
  // #endif

  // #ifndef H5
  uni.setClipboardData({ data: url, showToast: false });
  uni.showModal({
    title: "打开统一身份认证",
    content: "登录地址已复制。App 版发布前建议接入原生 OIDC 插件，登录完成后把 token 回传给应用。",
    showCancel: false,
  });
  loading.value = false;
  // #endif
}

async function loginWithToken() {
  await completeLoginWithToken(oidcToken.value);
}

async function completeLoginWithToken(token: string) {
  if (loading.value) return;
  errorMessage.value = "";
  loading.value = true;
  try {
    await saveBiographyOIDCToken(token);
    uni.redirectTo({ url: "/pages/interview/index" });
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : "统一身份认证失败，请重新登录";
  } finally {
    loading.value = false;
  }
}

function clearBiographyAuthIfLegacyPhoneLogin() {
  const current = currentBiographyAccessToken();
  if (!current) return;
  if (current.split(".").length === 3) return;
  clearBiographyAuth();
}
</script>

<template>
  <view class="login-page">
    <view class="login-card" :class="{ compact: !errorMessage && !devTokenInputEnabled }">
      <view class="brand-mark"><view /><view /><view /></view>
      <text v-if="!errorMessage" class="login-title">正在打开统一登录</text>
      <text v-if="!errorMessage" class="login-subtitle">请稍候，马上跳转。</text>
      <text v-if="errorMessage" class="error-message">{{ errorMessage }}</text>

      <button v-if="errorMessage" class="login-button" :disabled="loading" @click="startOIDCLogin">
        {{ loading ? "正在打开" : "重新打开统一登录" }}
      </button>

      <view v-if="devTokenInputEnabled" class="dev-token-panel">
        <text class="dev-token-title">开发调试</text>
        <textarea v-model="oidcToken" class="dev-token-input" maxlength="-1" placeholder="粘贴 OIDC access_token 或 id_token" />
        <button class="dev-token-button" :disabled="loading" @click="loginWithToken">使用此 token 继续</button>
      </view>
    </view>
  </view>
</template>

<style scoped>
.login-page {
  min-height: 100vh;
  min-height: 100dvh;
  display: grid;
  align-content: center;
  padding: 28px 22px;
  background: #f6f8f5;
}

.login-card {
  display: grid;
  gap: 18px;
  width: min(100%, 460px);
  margin: 0 auto;
  padding: 30px 24px;
  border: 1px solid #dfe8e2;
  border-radius: 24px;
  background: #fff;
  box-shadow: 0 18px 48px rgba(31, 78, 62, 0.1);
}
.login-card.compact {
  justify-items: center;
  width: min(100%, 280px);
  padding: 24px 20px;
  text-align: center;
}

.brand-mark {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 3px;
  width: 54px;
  height: 54px;
  border-radius: 14px;
  background: #1f7257;
}
.brand-mark view { width: 5px; border-radius: 3px; background: white; }
.brand-mark view:nth-child(1) { height: 18px; }
.brand-mark view:nth-child(2) { height: 31px; }
.brand-mark view:nth-child(3) { height: 23px; }

.login-title { color: #20352d; font-size: 30px; font-weight: 900; line-height: 1.1; }
.login-subtitle { color: #68776f; font-size: 16px; line-height: 1.6; }

.login-button,
.dev-token-button {
  min-height: 58px;
  margin: 0;
  border: 0;
  border-radius: 999px;
  font-weight: 850;
}
.login-button {
  margin-top: 4px;
  background: #1f7257;
  color: #fff;
  font-size: 18px;
  box-shadow: 0 12px 24px rgba(31, 114, 87, 0.18);
}
.login-button::after,
.dev-token-button::after { border: 0; }
.login-button[disabled],
.dev-token-button[disabled] { opacity: 0.6; }

.error-message { color: #a4493d; font-size: 14px; font-weight: 800; }

.dev-token-panel {
  display: grid;
  gap: 10px;
  padding: 14px;
  border: 1px dashed #cddbd4;
  border-radius: 18px;
  background: #fbfcfb;
}
.dev-token-title { color: #7b8a83; font-size: 13px; font-weight: 850; }
.dev-token-input {
  width: 100%;
  min-height: 96px;
  padding: 12px;
  box-sizing: border-box;
  border: 1px solid #d7e1dc;
  border-radius: 14px;
  background: #fff;
  color: #22362f;
  font-size: 14px;
}
.dev-token-button {
  background: #edf6f1;
  color: #1f7257;
  font-size: 15px;
}

@media (max-width: 420px) {
  .login-page { padding: 18px; }
  .login-card { padding: 26px 20px; border-radius: 22px; }
  .login-title { font-size: 28px; }
}
</style>

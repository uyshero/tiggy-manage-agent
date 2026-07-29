<script setup lang="ts">
import { onLaunch } from "@dcloudio/uni-app";

onLaunch(() => {
  // App-level initialization stays intentionally small; interview recovery is page-owned.
});
</script>

<style>
page {
  min-height: 100%;
  background: #f6f8f5;
  color: #1c2b27;
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif;
  font-size: 16px;
  letter-spacing: 0;
}

view,
text,
button {
  box-sizing: border-box;
}

button {
  font: inherit;
  letter-spacing: 0;
}
</style>

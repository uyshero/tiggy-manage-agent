import importlib.util
import pathlib
import unittest


MODULE_PATH = pathlib.Path(__file__).with_name("browser-plugin.py")
SPEC = importlib.util.spec_from_file_location("browser_plugin", MODULE_PATH)
PLUGIN = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PLUGIN)


class BrowserPluginTest(unittest.TestCase):
    def test_manifest_uses_browser_namespace_as_worker_plugin(self):
        self.assertEqual(PLUGIN.MANIFEST["identifier"], "browser")
        self.assertEqual(PLUGIN.MANIFEST["type"], "process_plugin")
        self.assertEqual(
            [api["name"] for api in PLUGIN.MANIFEST["api"]],
            ["open", "read", "click", "type", "screenshot", "takeover", "close"],
        )
        self.assertTrue(all(api["implementation"] == "worker_capability" for api in PLUGIN.MANIFEST["api"]))

    def test_click_and_type_require_selector_or_ref(self):
        by_name = {api["name"]: api for api in PLUGIN.MANIFEST["api"]}
        self.assertIn("anyOf", by_name["click"]["parameters"])
        self.assertIn("anyOf", by_name["type"]["parameters"])
        self.assertEqual(by_name["type"]["parameters"]["required"], ["text"])


if __name__ == "__main__":
    unittest.main()

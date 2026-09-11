import js from "@eslint/js";
import importX from "eslint-plugin-import-x";
import reactHooks from "eslint-plugin-react-hooks";
import { createTypeScriptImportResolver } from "eslint-import-resolver-typescript";
import globals from "globals";
import tseslint from "typescript-eslint";

const features = ["auth", "audit", "namespaces", "flows", "executions", "instances", "ai"];

export default tseslint.config(
  { ignores: ["dist", "node_modules", "src/routeTree.gen.ts", "src/api/**"] },
  {
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    files: ["**/*.{ts,tsx}"],
    languageOptions: { ecmaVersion: 2022, globals: globals.browser },
    plugins: { "react-hooks": reactHooks, "import-x": importX },
    settings: { "import-x/resolver-next": [createTypeScriptImportResolver()] },
    rules: {
      "react-hooks/rules-of-hooks": "error",
      "react-hooks/exhaustive-deps": "warn",
      "import-x/no-restricted-paths": [
        "error",
        {
          zones: [
            ...features.map((f) => ({
              target: `./src/features/${f}`,
              from: "./src/features",
              except: [`./${f}`],
            })),
            { target: "./src/features", from: ["./src/app", "./src/routes"] },
            { target: ["./src/components", "./src/lib"], from: ["./src/features", "./src/app", "./src/routes"] },
          ],
        },
      ],
    },
  },
);

// node_modules may ship stray .go files (e.g. flatted); a nested go.mod makes `go ... ./...`
// in the repo root skip the directory instead of failing to compile them.
import { existsSync, writeFileSync } from "node:fs";

if (existsSync("node_modules")) writeFileSync("node_modules/go.mod", "module ignore_node_modules\n");

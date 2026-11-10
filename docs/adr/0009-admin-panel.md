# ADR-0009. Админка

Статус: принято, 2026-11-10.

**Решение.** React + TypeScript + Vite + Mantine + TanStack. 3D-конвейер на three.js и @react-three/fiber. Сборка встраивается в бинарник и отдаётся gateway на `/`. Данные берутся из OpenAPI (`api/openapi.yaml`, снапшоты `/overview` и `/pipelines/{name}/conveyor`) и SSE.

**Последствия.** Экраны v1, привязанные к видео, не переносятся. Собственный UI модуля подключается ссылкой.

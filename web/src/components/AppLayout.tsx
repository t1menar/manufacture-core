import { ActionIcon, AppShell, Badge, Burger, Group, NavLink, Text, Title, Tooltip, useComputedColorScheme, useMantineColorScheme } from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { Link, Outlet, useRouterState } from "@tanstack/react-router";

import { useToken } from "../auth/token";
import { useLive } from "../live/store";
import { useLiveStream } from "../live/useLiveStream";
import { COLOR_SCHEME_KEY } from "../theme/theme";
import { LiveIndicator } from "./LiveIndicator";
import { LoginCard } from "./LoginCard";

const NAV = [
  { to: "/", label: "Конвейер", icon: "◈" },
  { to: "/pipelines", label: "Пайплайны", icon: "⇶" },
  { to: "/modules", label: "Модули", icon: "▣" },
  { to: "/runs", label: "Запуски", icon: "▶" },
  { to: "/audit", label: "Журнал", icon: "☰" },
  { to: "/settings", label: "Настройки", icon: "⚙" },
] as const;

function Shell() {
  useLiveStream();
  const [opened, { toggle, close }] = useDisclosure();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const { setColorScheme } = useMantineColorScheme();
  const computed = useComputedColorScheme("dark");
  const attention = useLive((s) => Object.values(s.items).filter((i) => i.state === "attention").length);
  const unauthorized = useLive((s) => s.connection === "unauthorized");

  const toggleTheme = () => {
    const next = computed === "dark" ? "light" : "dark";
    setColorScheme(next);
    try {
      localStorage.setItem(COLOR_SCHEME_KEY, next);
    } catch {
      /* ignore */
    }
  };

  return (
    <AppShell header={{ height: 56 }} navbar={{ width: 220, breakpoint: "sm", collapsed: { mobile: !opened } }} padding="md">
      <AppShell.Header>
        <Group h="100%" px="md" justify="space-between" wrap="nowrap">
          <Group gap="sm" wrap="nowrap">
            <Burger opened={opened} onClick={toggle} hiddenFrom="sm" size="sm" />
            <img src="/favicon.svg" width={26} height={26} alt="" />
            <Title order={4}>Manufacture</Title>
            {__MF_MOCK__ && (
              <Badge color="grape" variant="light" radius="sm">
                demo · симулятор
              </Badge>
            )}
          </Group>
          <Group gap="md" wrap="nowrap">
            <LiveIndicator />
            <Tooltip label={computed === "dark" ? "Светлая тема" : "Тёмная тема"}>
              <ActionIcon variant="subtle" onClick={toggleTheme} aria-label="Переключить тему" size="lg">
                {computed === "dark" ? "☀" : "☾"}
              </ActionIcon>
            </Tooltip>
          </Group>
        </Group>
      </AppShell.Header>
      <AppShell.Navbar p="sm">
        {NAV.map((item) => {
          const active = item.to === "/" ? pathname === "/" : pathname.startsWith(item.to);
          return (
            <NavLink
              key={item.to}
              component={Link}
              to={item.to}
              label={item.label}
              leftSection={<Text span w={18} ta="center">{item.icon}</Text>}
              rightSection={item.to === "/runs" && attention > 0 ? <Badge size="xs" color="orange" circle>{attention}</Badge> : undefined}
              active={active}
              onClick={close}
            />
          );
        })}
      </AppShell.Navbar>
      <AppShell.Main>{unauthorized && pathname !== "/settings" ? <LoginCard /> : <Outlet />}</AppShell.Main>
    </AppShell>
  );
}

export function AppLayout() {
  const token = useToken();
  if (!token) {
    return (
      <AppShell padding="md">
        <AppShell.Main>
          <LoginCard />
        </AppShell.Main>
      </AppShell>
    );
  }
  return <Shell />;
}

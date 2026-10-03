import type { PublicState } from "./types";

export function formatWorldClock(clock: string) {
  const absolute = /^(\d+)-(\d{2})-(\d{2}) (\d{2}:\d{2})$/.exec(clock);
  if (absolute) return `${Number(absolute[1])}年${Number(absolute[2])}月${Number(absolute[3])}日 · ${absolute[4]}`;
  const time = /(?:^|\s)(\d{2}:\d{2})$/.exec(clock);
  return time ? `日期未配置 · ${time[1]}` : "日期未配置";
}

export function stateDisplayValue(state: PublicState) {
  if (state.display_value !== undefined) return state.display_value;
  if (state.value.type === "integer") return `${state.value.integer ?? 0}${state.unit ?? ""}`;
  if (state.value.type === "boolean") return state.value.boolean ? "是" : "否";
  return state.value.enum ?? "—";
}

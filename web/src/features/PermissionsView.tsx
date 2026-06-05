import type { AppData } from "../app/types";
import { ContextList, Panel, Pill } from "../components/ui";
import { defaultProfiles, profileLabel } from "../domain/labels";

export function PermissionsView({ data }: { data: AppData }) {
  const profiles = data.permissionProfiles.length ? data.permissionProfiles : defaultProfiles();
  return (
    <div className="grid min-h-[calc(100dvh-104px)] grid-cols-[minmax(0,1fr)_332px] max-xl:grid-cols-1">
      <div className="p-5">
        <Panel subtitle="MVP 先展示能力边界，后续再接入策略编辑和审批决策。" title="权限模式">
          <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-3">
            {profiles.map((profile) => (
              <article className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3" key={profile.name}>
                <div className="flex items-start justify-between gap-3">
                  <strong>{profileLabel(profile.name)}</strong>
                  <Pill tone={profile.risk === "low" ? "good" : profile.risk === "medium" ? "warn" : "danger"}>{profile.risk || "low"}</Pill>
                </div>
                <p className="muted mt-3 mb-0 text-sm">{profile.description || "该权限模式暂未配置说明。"}</p>
              </article>
            ))}
          </div>
        </Panel>
      </div>
      <aside className="border-l border-[var(--line)] bg-[var(--surface-soft)] p-5 max-xl:border-l-0 max-xl:border-t">
        <Panel title="Policy Chain">
          <ContextList
            items={[
              ["身份", "Owner"],
              ["资源边界", "Allowed roots"],
              ["命令策略", "Allow / Prompt / Deny"],
              ["待审批", data.pendingApprovals.length],
            ]}
          />
        </Panel>
      </aside>
    </div>
  );
}

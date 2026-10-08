import { useTranslation } from "react-i18next";
import { missingRequiredParams } from "@/lib/params";
import type { Param } from "@/lib/types";
import { Input } from "./ui/input";
import { Label } from "./ui/label";
import { Select } from "./ui/select";
import { Switch } from "./ui/switch";

export interface ParamFormProps {
  params: Param[];
  values: Record<string, string>;
  onChange: (values: Record<string, string>) => void;
  showErrors?: boolean;
  idPrefix?: string;
}

export default function ParamForm({
  params,
  values,
  onChange,
  showErrors = false,
  idPrefix = "param",
}: ParamFormProps) {
  const { t } = useTranslation();

  const setValue = (id: string, value: string) => {
    onChange({ ...values, [id]: value });
  };

  return (
    <div className="space-y-3">
      {params.map((param) => {
        const fieldId = `${idPrefix}-${param.id}`;
        const label = param.label ?? param.id;
        const value = values[param.id] ?? "";
        const missing = showErrors && missingRequiredParams([param], values).length > 0;
        return (
          <div key={param.id} className="space-y-1">
            <div className="flex items-center gap-2">
              {param.type === "boolean" ? (
                <Switch
                  id={fieldId}
                  aria-label={label}
                  checked={value === "true"}
                  onCheckedChange={(checked) => setValue(param.id, String(checked))}
                />
              ) : null}
              <Label htmlFor={fieldId}>{label}</Label>
              {param.required && (
                <span aria-hidden="true" className="text-destructive">
                  *
                </span>
              )}
              <span className="text-xs text-muted-foreground">
                {t(`params.scope.${param.scope}`)}
              </span>
            </div>
            {param.type === "string" && (
              <Input
                id={fieldId}
                type={param.secret ? "password" : "text"}
                value={value}
                aria-invalid={missing || undefined}
                onChange={(e) => setValue(param.id, e.target.value)}
              />
            )}
            {param.type === "number" && (
              <Input
                id={fieldId}
                type="number"
                value={value}
                aria-invalid={missing || undefined}
                onChange={(e) => setValue(param.id, e.target.value)}
              />
            )}
            {param.type === "select" && (
              <Select
                id={fieldId}
                value={value}
                aria-invalid={missing || undefined}
                onChange={(e) => setValue(param.id, e.target.value)}
              >
                <option value="">{t("params.selectPlaceholder")}</option>
                {(param.options ?? []).map((opt) => (
                  <option key={opt} value={opt}>
                    {opt}
                  </option>
                ))}
              </Select>
            )}
            {missing && <p className="text-xs text-destructive">{t("params.required")}</p>}
          </div>
        );
      })}
    </div>
  );
}

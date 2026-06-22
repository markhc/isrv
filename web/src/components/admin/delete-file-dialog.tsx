import * as AlertDialog from "@radix-ui/react-alert-dialog"
import { useMutation } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { deleteFile, type AdminFile } from "@/lib/admin-api"

interface DeleteFileDialogProps {
  file: AdminFile | null
  onClose: () => void
  onDeleted: () => void
}

export function DeleteFileDialog({ file, onClose, onDeleted }: DeleteFileDialogProps) {
  const { t } = useTranslation("admin")

  const mutation = useMutation({
    mutationFn: (id: string) => deleteFile(id),
    onSuccess: () => {
      toast.success(t("delete.success"))
      onDeleted()
      onClose()
    },
    onError: (err: Error) => {
      toast.error(err.message || t("delete.failed"))
    },
  })

  return (
    <AlertDialog.Root open={file !== null} onOpenChange={(open) => !open && onClose()}>
      <AlertDialog.Portal>
        <AlertDialog.Overlay className="fixed inset-0 z-50 bg-black/50" />
        <AlertDialog.Content className="fixed left-1/2 top-1/2 z-50 w-full max-w-md -translate-x-1/2 -translate-y-1/2 rounded-lg border border-border bg-card p-6 shadow-lg">
          <AlertDialog.Title className="text-lg font-semibold">
            {t("delete.title")}
          </AlertDialog.Title>
          <AlertDialog.Description className="mt-2 text-sm text-muted-foreground">
            {t("delete.description", { name: file?.name ?? "" })}
          </AlertDialog.Description>
          <div className="mt-6 flex justify-end gap-3">
            <AlertDialog.Cancel className="rounded-md border border-border px-3 py-2 text-sm transition-colors hover:bg-muted">
              {t("delete.cancel")}
            </AlertDialog.Cancel>
            <button
              type="button"
              disabled={mutation.isPending}
              onClick={() => file && mutation.mutate(file.id)}
              className="rounded-md bg-destructive px-3 py-2 text-sm font-medium text-destructive-foreground transition-colors hover:bg-destructive/90 disabled:opacity-60"
            >
              {mutation.isPending ? t("delete.deleting") : t("delete.confirm")}
            </button>
          </div>
        </AlertDialog.Content>
      </AlertDialog.Portal>
    </AlertDialog.Root>
  )
}

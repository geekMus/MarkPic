import { copyText } from '../composables/useDeadLinks'
import { useToasts } from '../composables/useToasts'
import { useI18n } from '../i18n'

/**
 * 点一行地址就进剪贴板：贴到别处查比手动选中省事。
 *
 * 成功和失败都要说一句 —— 剪贴板在没聚焦的窗口里会静默失败，
 * 不吭声的话用户会以为复制成功了，粘出来才发现是空的。
 */
export function useCopyAddress() {
  const { t } = useI18n()
  const { success, danger } = useToasts()

  async function copyAddress(text: string): Promise<void> {
    if (await copyText(text)) success(t('toast.copied'))
    else danger(t('toast.copyFailed'))
  }

  return { copyAddress }
}

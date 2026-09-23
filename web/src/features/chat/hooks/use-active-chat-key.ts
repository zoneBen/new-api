/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useQuery } from '@tanstack/react-query'
import { t } from 'i18next'
import { useCallback } from 'react'

import {
  useSecureVerification,
  type SecurityProof,
} from '@/features/auth/secure-verification'
import { fetchTokenKey, getApiKeys } from '@/features/keys/api'
import { API_KEY_STATUS } from '@/features/keys/constants'
import { AuthOperationError } from '@/lib/secure-verification'
import {
  requireServerSuccess,
  createServerError,
} from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

async function findActiveChatKeyId(): Promise<number> {
  const result = await getApiKeys({ p: 1, size: 50 })
  if (!result.success) {
    throw createServerError(result, t('Failed to load API keys'))
  }

  const items = result.data?.items ?? []
  const active = items.find((item) => item.status === API_KEY_STATUS.ENABLED)
  if (!active) {
    throw new Error('No enabled API keys found. Create or enable one first.')
  }

  return active.id
}

/**
 * Resolve the active key through a step-up proof. The proof is bound to the key
 * id, so the id has to be known first and only the caller can raise the dialog.
 */
export async function fetchActiveChatKey(
  requestProof: (tokenIds: number[]) => Promise<SecurityProof | null>
): Promise<string> {
  const keyId = await findActiveChatKeyId()
  const proof = await requestProof([keyId])
  if (!proof) {
    throw new AuthOperationError(
      t('Verification cancelled. The chat link was not created.'),
      'AUTH_CANCELLED'
    )
  }

  const keyResult = await fetchTokenKey(keyId, proof.proof_token)
  if (!keyResult.success || !keyResult.data?.key) {
    throw createServerError(keyResult, t('Failed to load API keys'))
  }

  return `sk-${keyResult.data.key}`
}

export function useChatKeyProofRequest() {
  const { requestVerification, dialogProps } = useSecureVerification()
  const requestProof = useCallback(
    (tokenIds: number[]) =>
      requestVerification({
        scope: 'token.key.read',
        context: { token_ids: tokenIds },
        title: t('Verify to view API key'),
        description: t('Confirm your identity before revealing this API key.'),
      }),
    [requestVerification]
  )
  return { requestProof, dialogProps }
}

/**
 * Get the currently active API key for chat links
 */
export function useActiveChatKey(enabled: boolean) {
  const userId = useAuthStore((state) => state.auth.user?.id)
  const { requestProof, dialogProps } = useChatKeyProofRequest()

  const query = useQuery({
    queryKey: ['chat-active-key', userId],
    queryFn: async () =>
      requireServerSuccess(await fetchActiveChatKey(requestProof)),
    enabled: enabled && Boolean(userId),
    // A prompt is one user decision, not a retry loop: a refused or cancelled
    // verification must not open the dialog again behind the user's back.
    retry: false,
    staleTime: 5 * 60 * 1000,
    gcTime: 10 * 60 * 1000,
  })

  return { ...query, dialogProps }
}

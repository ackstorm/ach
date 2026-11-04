-- SPDX-License-Identifier: Apache-2.0

-- Environment rows left by finalizer drains that only marked them deleted.
-- Their CR, LiteLLM access group and keys are already gone.
DELETE FROM environments WHERE deletion_timestamp IS NOT NULL;

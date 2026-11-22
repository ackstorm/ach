-- SPDX-License-Identifier: Apache-2.0
ALTER TABLE environments RENAME COLUMN context_instructions TO context_prompts;

ALTER TABLE instructions ADD COLUMN content_type text NULL;
ALTER TABLE instructions RENAME CONSTRAINT instructions_cr_locked_chk TO prompts_cr_locked_chk;
ALTER TABLE instructions RENAME CONSTRAINT instructions_origin_chk TO prompts_origin_chk;
ALTER TABLE instructions RENAME CONSTRAINT instructions_pkey TO prompts_pkey;
ALTER TABLE instructions RENAME TO prompts;

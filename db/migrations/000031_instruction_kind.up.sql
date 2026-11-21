-- SPDX-License-Identifier: Apache-2.0
-- Instruction replaces Prompt: same projection shape without content_type.
-- Prompt data does not carry over: it never passed the Instruction checks.
ALTER TABLE prompts RENAME TO instructions;
ALTER TABLE instructions RENAME CONSTRAINT prompts_pkey TO instructions_pkey;
ALTER TABLE instructions RENAME CONSTRAINT prompts_origin_chk TO instructions_origin_chk;
ALTER TABLE instructions RENAME CONSTRAINT prompts_cr_locked_chk TO instructions_cr_locked_chk;
ALTER TABLE instructions DROP COLUMN content_type;
DELETE FROM instructions;

ALTER TABLE environments RENAME COLUMN context_prompts TO context_instructions;
UPDATE environments
   SET context_instructions = '{}',
       spec = CASE WHEN spec IS NULL THEN NULL ELSE spec #- '{context,prompts}' END
 WHERE context_instructions <> '{}' OR spec->'context' ? 'prompts';

DELETE FROM external_refs WHERE kind = 'prompt';

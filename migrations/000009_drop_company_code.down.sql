-- Restores a company code, filled from the start of the company ID.
ALTER TABLE companies ADD COLUMN code text;
UPDATE companies SET code = upper(left(id::text, 8));
ALTER TABLE companies
    ALTER COLUMN code SET NOT NULL,
    ADD CHECK (btrim(code) <> '' AND char_length(code) <= 32);

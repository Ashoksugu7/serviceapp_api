-- Products can be selected on more than one service profile.
CREATE TABLE product_service_profiles (
    company_id uuid NOT NULL,
    product_id uuid NOT NULL,
    profile_id uuid NOT NULL,
    PRIMARY KEY (company_id, product_id, profile_id),
    FOREIGN KEY (company_id, product_id) REFERENCES products(company_id, id) ON DELETE CASCADE,
    FOREIGN KEY (company_id, profile_id) REFERENCES service_profiles(company_id, id)
);

INSERT INTO product_service_profiles(company_id, product_id, profile_id)
SELECT company_id, id, profile_id FROM products;

CREATE INDEX product_service_profiles_profile_idx ON product_service_profiles (company_id, profile_id, product_id);

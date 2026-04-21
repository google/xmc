ALTER TABLE Artifacts DROP COLUMN AdditionalInfo;

ALTER PROTO BUNDLE DELETE (
  cloud.xmanager.v1.Artifact.AdditionalInfo
);

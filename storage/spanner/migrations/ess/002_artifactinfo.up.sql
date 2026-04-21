ALTER PROTO BUNDLE INSERT (
  cloud.xmanager.v1.Artifact.AdditionalInfo
);

ALTER TABLE Artifacts ADD COLUMN AdditionalInfo cloud.xmanager.v1.Artifact.AdditionalInfo;

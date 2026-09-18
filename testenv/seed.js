const target = db.getSiblingDB("nhn-ror");
if (target.acl.countDocuments({$or: [{_id: {$in: ["e2e-admin", "e2e-reader"]}}, {group: /@e2e\.invalid$/}]}) !== 0) {
  throw new Error("Fixture identity collision: snapshot must not contain the reserved e2e.invalid realm");
}
target.acl.insertMany([
  {_id: "e2e-admin", version: 3, group: "admins@e2e.invalid", scope: "ror", subject: "all", access: ["ror:read", "ror:create", "ror:update", "ror:delete", "ror:config:read", "ror:config:write"], created: ISODate("2026-01-01T00:00:00Z")},
  {_id: "e2e-reader", version: 3, group: "readers@e2e.invalid", scope: "KubernetesCluster", subject: "11111111-1111-4111-8111-111111111111", access: ["ror:read"], created: ISODate("2026-01-01T00:00:00Z")}
]);
const crypto = require("crypto");
const clusters = [
  {name: "a", uid: "11111111-1111-4111-8111-111111111111", pod: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", deployment: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab"},
  {name: "b", uid: "22222222-2222-4222-8222-222222222222", pod: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", deployment: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbc"}
];
for (const cluster of clusters) {
  const identifier = `e2e-cluster-${cluster.name}`;
  const group = `${cluster.uid}@cluster.ror.system`;
  const resourceUIDs = [cluster.uid, cluster.pod, cluster.deployment];
  if (target.apikeys.countDocuments({$or: [{identifier}, {uid: cluster.uid}]}) !== 0 ||
      target.acl.countDocuments({group}) !== 0 ||
      target.resourcesv2.countDocuments({uid: {$in: resourceUIDs}}) !== 0) {
    throw new Error("Cluster fixture collision: reserved test identities/resources already exist");
  }
  target.apikeys.insertOne({
    _id: identifier, identifier, type: "Cluster", uid: cluster.uid,
    hash: crypto.createHash("sha512").update(`${identifier}-synthetic-onlysynthetic-only`).digest("hex"),
    expires: ISODate("2099-01-01T00:00:00Z"), created: ISODate("2026-01-01T00:00:00Z")
  });
  target.acl.insertOne({
    _id: identifier, version: 3, group, scope: "KubernetesCluster", subject: cluster.uid,
    access: ["ror:read", "ror:create", "ror:update"], created: ISODate("2026-01-01T00:00:00Z")
  });
  for (const [kind, uid, apiVersion] of [
    ["KubernetesCluster", cluster.uid, "general.ror.internal/v1alpha1"],
    ["Pod", cluster.pod, "v1"],
    ["Deployment", cluster.deployment, "apps/v1"]
  ]) {
    target.resourcesv2.insertOne({
      uid,
      typemeta: {kind, apiversion: apiVersion},
      metadata: {uid, name: `${identifier}-${kind.toLowerCase()}`, namespace: "default", labels: {"e2e-update": "original"}},
      rormeta: {version: "v2", hash: "synthetic", ownerref: {scope: "KubernetesCluster", subject: cluster.uid}}
    });
  }
}
print("Seeded synthetic ACL and cluster-resource fixture v2");